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
	log.Printf("[sfu] http=%s udp=%s public=%s:%d", httpAddr, udp.LocalAddr(), publicIP, port)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/sessions", srv.handleNewSession)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.udpLoop()
	go srv.statsLoop(ctx)
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

	sess := &Session{
		ID:               uuid.NewString(),
		LocalUfrag:       RandomUfrag(),
		LocalPwd:         RandomPwd(),
		RemoteUfrag:      offer.IceUfrag,
		RemotePwd:        offer.IcePwd,
		RemoteFinger:     offer.Fingerprint,
		LocalCert:        cert,
		LocalFingerprint: fp,
	}
	s.sessions.Add(sess)

	answer := BuildAnswer(offer, AnswerParams{
		IceUfrag:    sess.LocalUfrag,
		IcePwd:      sess.LocalPwd,
		Fingerprint: fp,
		HostIP:      s.publicIP,
		HostPort:    s.udpPort,
	})
	log.Printf("[sfu] session created id=%s ufrag=%s remoteUfrag=%s fp=%s…",
		sess.ID, sess.LocalUfrag, sess.RemoteUfrag, fp[:24])

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(answerResp{Type: "answer", SDP: answer, SessionID: sess.ID})
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
			log.Printf("[sfu] dtls drop no-session from=%s", from)
			return
		}
		sess.mu.Lock()
		pipe := sess.dtlsPipe
		sess.mu.Unlock()
		if pipe != nil {
			log.Printf("[sfu] dtls push from=%s len=%d", from, len(raw))
			pipe.Push(raw)
		} else {
			log.Printf("[sfu] dtls drop no-pipe from=%s", from)
		}
	default:
		// SRTP/RTCP entram na Etapa 7.
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
			log.Printf("[sfu] stats stun_in=%d stun_out=%d dtls_in=%d dtls_ok=%d", stunIn.Load(), stunOut.Load(), dtlsIn.Load(), dtlsHS.Load())
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
