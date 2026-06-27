// sfu — Selective Forwarding Unit própria.
//
// Etapa 5: ICE-lite + negociação SDP.
//   - HTTP POST /sessions: recebe { sdp, type:"offer" } → devolve { sdp, type:"answer", sessionId }
//   - UDP listener single-port: responde STUN Binding requests com USERNAME validado
//   - Fingerprint do answer ainda é placeholder (DTLS entra na Etapa 6)
//
// Configuração via env:
//
//	SFU_HTTP_ADDR   default ":8081"
//	SFU_UDP_ADDR    default ":7000"
//	SFU_PUBLIC_IP   IP anunciado nos candidatos host
package main

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
)

const softwareName = "webrtc-own-sfu/0.1"

type Server struct {
	udp      *net.UDPConn
	publicIP string
	udpPort  int
	sessions *SessionStore
	router   *Router
}

var (
	stunIn  atomic.Uint64
	stunOut atomic.Uint64
	dtlsIn  atomic.Uint64
	dtlsHS  atomic.Uint64
)

func main() {
	httpAddr := getenv("SFU_HTTP_ADDR", ":8081")
	udpAddr := getenv("SFU_UDP_ADDR", ":7000")

	udp, err := net.ListenUDP("udp", mustResolveUDP(udpAddr))
	if err != nil {
		log.Fatalf("[sfu] udp listen: %v", err)
	}
	publicIP := os.Getenv("SFU_PUBLIC_IP")
	if publicIP == "" {
		publicIP = detectPublic()
	}
	port := udp.LocalAddr().(*net.UDPAddr).Port

	srv := &Server{udp: udp, publicIP: publicIP, udpPort: port, sessions: newSessionStore()}
	srv.router = NewRouter(udp)
	log.Printf("[sfu] http=%s udp=%s public=%s:%d", httpAddr, udp.LocalAddr(), publicIP, port)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/sessions", srv.handleNewSession)
	mux.HandleFunc("/sessions/", srv.handleSessionSub)


	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.udpLoop()
	go srv.statsLoop(ctx)
	srv.router.StartFeedbackLoop(ctx)
	go shutdown(cancel, udp)


	if err := http.ListenAndServe(httpAddr, mux); err != nil {
		log.Fatalf("[sfu] http: %v", err)
	}
}

type offerReq struct {
	Type string `json:"type"`
	SDP  string `json:"sdp"`
}
type answerResp struct {
	Type      string `json:"type"`
	SDP       string `json:"sdp"`
	SessionID string `json:"sessionId"`
}

func (s *Server) handleNewSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	var req offerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if req.Type != "offer" || req.SDP == "" {
		http.Error(w, "expected offer", http.StatusBadRequest)
		return
	}
	offer, err := ParseOffer(req.SDP)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if offer.IceUfrag == "" || offer.IcePwd == "" {
		http.Error(w, "missing ice creds in offer", http.StatusBadRequest)
		return
	}
	if offer.Fingerprint == "" {
		http.Error(w, "missing fingerprint in offer", http.StatusBadRequest)
		return
	}

	cert, err := generateDTLSCert()
	if err != nil {
		http.Error(w, "cert gen: "+err.Error(), http.StatusInternalServerError)
		return
	}
	fp := FingerprintSHA256(cert.Certificate[0])

	// Pega RIDExtID/TWCCExtID e RIDs do primeiro m-line de vídeo.
	// Agrega PT→codec de TODOS os m-lines (BUNDLE: PTs são únicos).
	var ridExt, rridExt, twccExt uint8
	var rids []string
	ptCodec := map[uint8]string{}
	ptClock := map[uint8]uint32{}
	for _, m := range offer.Media {
		if m.TWCCExtID != 0 && twccExt == 0 {
			twccExt = m.TWCCExtID
		}
		if m.Kind == "video" && len(m.RIDs) > 0 && ridExt == 0 {
			ridExt = m.RIDExtID
			rridExt = m.RRIDExtID
			rids = m.RIDs
		}
		for pt, name := range m.Rtpmap {
			ptCodec[pt] = name
		}
		for pt, clk := range m.ClockRate {
			ptClock[pt] = clk
		}
	}


	sess := &Session{
		ID:               uuid.NewString(),
		LocalUfrag:       RandomUfrag(),
		LocalPwd:         RandomPwd(),
		RemoteUfrag:      offer.IceUfrag,
		RemotePwd:        offer.IcePwd,
		RemoteFinger:     offer.Fingerprint,
		LocalCert:        cert,
		LocalFingerprint: fp,
		RIDExtID:         ridExt,
		RRIDExtID:        rridExt,
		TWCCExtID:        twccExt,
		OfferedRIDs:      rids,
		PTCodec:          ptCodec,
		PTClock:          ptClock,
	}


	s.sessions.Add(sess)

	answer := BuildAnswer(offer, AnswerParams{
		IceUfrag:    sess.LocalUfrag,
		IcePwd:      sess.LocalPwd,
		Fingerprint: fp,
		HostIP:      s.publicIP,
		HostPort:    s.udpPort,
	})
	log.Printf("[sfu] session created id=%s ufrag=%s remoteUfrag=%s fp=%s… rids=%v ridExt=%d",
		sess.ID, sess.LocalUfrag, sess.RemoteUfrag, fp[:24], rids, ridExt)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(answerResp{Type: "answer", SDP: answer, SessionID: sess.ID})
}

// POST /sessions/{id}/layer            { publisherId, rid }
// POST /sessions/{id}/record/start
// POST /sessions/{id}/record/stop
// GET  /sessions/{id}/record            → manifesto JSON
type layerReq struct {
	PublisherID string `json:"publisherId"`
	RID         string `json:"rid"`
}

func (s *Server) handleSessionSub(w http.ResponseWriter, r *http.Request) {
	const prefix = "/sessions/"
	path := r.URL.Path
	if len(path) <= len(prefix) || path[:len(prefix)] != prefix {
		http.NotFound(w, r)
		return
	}
	rest := path[len(prefix):]
	// rest = "{id}/{sub...}"
	slash := -1
	for i := 0; i < len(rest); i++ {
		if rest[i] == '/' {
			slash = i
			break
		}
	}
	if slash < 0 {
		http.NotFound(w, r)
		return
	}
	id := rest[:slash]
	sub := rest[slash+1:]

	switch sub {
	case "layer":
		s.handleSwitchLayer(w, r, id)
	case "record/start":
		s.handleRecordStart(w, r, id)
	case "record/stop":
		s.handleRecordStop(w, r, id)
	case "record":
		s.handleRecordGet(w, r, id)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleSwitchLayer(w http.ResponseWriter, r *http.Request, subID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	var req layerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if req.PublisherID == "" || req.RID == "" {
		http.Error(w, "publisherId and rid required", http.StatusBadRequest)
		return
	}
	ssrc, err := s.router.SwitchLayer(subID, req.PublisherID, req.RID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":         true,
		"targetSSRC": ssrc,
		"rid":        req.RID,
	})
}

func (s *Server) handleRecordStart(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if s.sessions.ByID(id) == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	if err := s.router.rec.Start(id); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	writeManifest(w, s.router.rec, id)
}

func (s *Server) handleRecordStop(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if err := s.router.rec.Stop(id); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeManifest(w, s.router.rec, id)
}

func (s *Server) handleRecordGet(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	writeManifest(w, s.router.rec, id)
}

func writeManifest(w http.ResponseWriter, h *RecorderHub, id string) {
	if !h.Enabled() {
		http.Error(w, "recorder disabled (set SFU_RECORD_DIR)", http.StatusServiceUnavailable)
		return
	}
	m, err := h.Manifest(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(m)
}


func (s *Server) udpLoop() {
	buf := make([]byte, 1500)
	for {
		n, from, err := s.udp.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && !ne.Timeout() {
				log.Printf("[sfu] udp read: %v", err)
			}
			return
		}
		raw := make([]byte, n)
		copy(raw, buf[:n])
		s.handlePacket(raw, from)
	}
}

func (s *Server) handlePacket(raw []byte, from *net.UDPAddr) {
	switch {
	case IsSTUN(raw):
		stunIn.Add(1)
		resp, _ := s.HandleBinding(raw, from)
		if resp != nil {
			if _, err := s.udp.WriteToUDP(resp, from); err == nil {
				stunOut.Add(1)
			}
		}
	case IsDTLS(raw):
		dtlsIn.Add(1)
		sess := s.sessions.ByAddr(from.String())
		if sess == nil {
			return
		}
		sess.mu.Lock()
		pipe := sess.dtlsPipe
		sess.mu.Unlock()
		if pipe != nil {
			pipe.Push(raw)
		}
	case IsRTPOrRTCP(raw):
		sess := s.sessions.ByAddr(from.String())
		if sess == nil {
			return
		}
		if IsRTCP(raw) {
			if s.router != nil {
				s.router.HandleRTCP(sess, raw)
			}
			return
		}
		if s.router != nil {
			s.router.HandleRTP(sess, raw)
		}
	default:
		// Outros bytes (TURN ChannelData) não aplicam aqui.
	}
}

func (s *Server) statsLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			log.Printf("[sfu] stats stun_in=%d stun_out=%d dtls_in=%d dtls_ok=%d rtp_in=%d rtp_fwd=%d rtp_drop=%d rtcp_in=%d rtcp_fwd=%d rtcp_fb=%d rtx_hit=%d rtx_miss=%d twcc_out=%d twcc_in=%d remb_out=%d layer_auto=%d sctp=%d dc=%d dc_in=%d dc_fwd=%d",
				stunIn.Load(), stunOut.Load(), dtlsIn.Load(), dtlsHS.Load(),
				rtpIn.Load(), rtpFwd.Load(), rtpDrop.Load(),
				rtcpIn.Load(), rtcpFwd.Load(), rtcpFB.Load(),
				rtxHit.Load(), rtxMiss.Load(),
				twccSent.Load(), twccRecv.Load(), rembSent.Load(), layerAuto.Load(),
				sctpAssoc.Load(), dcChans.Load(), dcMsgIn.Load(), dcMsgFwd.Load())


		}
	}
}

func shutdown(cancel context.CancelFunc, conn *net.UDPConn) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	cancel()
	_ = conn.Close()
}

func mustResolveUDP(a string) *net.UDPAddr {
	addr, err := net.ResolveUDPAddr("udp", a)
	if err != nil {
		log.Fatalf("[sfu] resolve %s: %v", a, err)
	}
	return addr
}
func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
func detectPublic() string {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok {
			if ip4 := ipn.IP.To4(); ip4 != nil && !ip4.IsLoopback() {
				return ip4.String()
			}
		}
	}
	return "127.0.0.1"
}
