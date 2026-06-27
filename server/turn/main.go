// turn — servidor TURN próprio (RFC 5766 + 8656).
//
// Cobertura desta etapa:
//   - Allocate / Refresh com long-term auth (REALM + NONCE + MESSAGE-INTEGRITY)
//   - CreatePermission, ChannelBind
//   - Send indication (cliente → peer)
//   - Data indication (peer → cliente)
//   - ChannelData (caminho rápido bidirecional)
//   - Binding request (compatibilidade STUN)
//   - Relay UDP-UDP. TCP/TLS ficam pra depois.
//
// Configuração via env:
//
//	TURN_ADDR          (default :3478)
//	TURN_PUBLIC_IP     IP anunciado em XOR-RELAYED-ADDRESS (default: detecta listen)
//	TURN_REALM         (default "webrtc-own")
//	TURN_STATIC_USER / TURN_STATIC_PASS  credencial fixa
//	TURN_AUTH_SECRET   liga ephemeral creds (formato coturn)
package main

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"errors"
	"log"
	"net"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
)

const softwareName = "webrtc-own-turn/0.1"

var (
	errAllocationMismatch = errors.New("allocation mismatch")
	errChannelInUse       = errors.New("channel in use")
)

var (
	pktIn  atomic.Uint64
	pktOut atomic.Uint64
	relayIn atomic.Uint64
	relayOut atomic.Uint64
)

// Server agrupa o socket de escuta TURN + manager + credenciais.
type Server struct {
	conn   *net.UDPConn
	allocs *allocManager
	creds  *credStore
}

func main() {
	addr := os.Getenv("TURN_ADDR")
	if addr == "" {
		addr = ":3478"
	}
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		log.Fatalf("[turn] resolve: %v", err)
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		log.Fatalf("[turn] listen: %v", err)
	}
	listenHost := "0.0.0.0"
	if udpAddr.IP != nil && !udpAddr.IP.IsUnspecified() {
		listenHost = udpAddr.IP.String()
	}
	publicIP := net.ParseIP(os.Getenv("TURN_PUBLIC_IP"))
	if publicIP == nil {
		publicIP = detectPublic(listenHost)
	}
	log.Printf("[turn] listening UDP %s, relay public IP %s", conn.LocalAddr(), publicIP)

	srv := &Server{
		conn:   conn,
		allocs: newAllocManager(publicIP, listenHost),
		creds:  newCredStore(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.gcLoop(ctx)
	go shutdownOnSignal(cancel, conn)
	srv.serve()
}

func detectPublic(listenHost string) net.IP {
	ip := net.ParseIP(listenHost)
	if ip != nil && !ip.IsUnspecified() {
		return ip
	}
	// Pega primeira interface não-loopback.
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok {
			if ip4 := ipn.IP.To4(); ip4 != nil && !ip4.IsLoopback() {
				return ip4
			}
		}
	}
	return net.IPv4(127, 0, 0, 1)
}

func (s *Server) serve() {
	buf := make([]byte, 65535)
	for {
		n, from, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && !ne.Timeout() {
				log.Printf("[turn] read: %v", err)
			}
			return
		}
		pktIn.Add(1)
		raw := make([]byte, n)
		copy(raw, buf[:n])
		s.dispatch(raw, from)
	}
}

func (s *Server) dispatch(raw []byte, from *net.UDPAddr) {
	if IsChannelData(raw) {
		s.handleChannelData(raw, from)
		return
	}
	msg, err := Decode(raw)
	if err != nil {
		return
	}
	method := methodOf(msg.Type)
	class := classOf(msg.Type)
	if class != classRequest && class != classIndication {
		return
	}
	switch method {
	case methodBinding:
		if class == classRequest {
			s.handleBinding(msg, from)
		}
	case methodAllocate:
		s.handleAllocate(raw, msg, from)
	case methodRefresh:
		s.handleRefresh(raw, msg, from)
	case methodCreatePermission:
		s.handleCreatePermission(raw, msg, from)
	case methodChannelBind:
		s.handleChannelBind(raw, msg, from)
	case methodSend:
		if class == classIndication {
			s.handleSendIndication(msg, from)
		}
	}
}

func (s *Server) handleBinding(msg *Message, from *net.UDPAddr) {
	resp := &Message{Type: msgType(methodBinding, classSuccess), TransactionID: msg.TransactionID}
	resp.Add(AttrXORMappedAddress, EncodeXORAddr(from, msg.TransactionID))
	resp.Add(AttrSoftware, []byte(softwareName))
	out := AppendFingerprint(resp.Encode())
	s.send(out, from)
}

// authenticate valida REALM + NONCE + MESSAGE-INTEGRITY. Se faltar, devolve
// 401 com REALM/NONCE; se nonce stale, 438; se assinatura inválida, 401.
// Retorna (key, username, ok). Quando !ok, já respondeu.
func (s *Server) authenticate(raw []byte, msg *Message, from *net.UDPAddr) ([]byte, string, bool) {
	userV, hasUser := msg.Get(AttrUsername)
	realmV, hasRealm := msg.Get(AttrRealm)
	nonceV, hasNonce := msg.Get(AttrNonce)
	_, hasMI := msg.Get(AttrMessageIntegrity)
	if !hasUser || !hasRealm || !hasNonce || !hasMI {
		s.sendError(msg, from, 401, "Unauthorized", true)
		return nil, "", false
	}
	if string(realmV) != s.creds.realm {
		s.sendError(msg, from, 441, "Wrong Credentials", true)
		return nil, "", false
	}
	if !s.creds.validNonce(string(nonceV)) {
		s.sendError(msg, from, 438, "Stale Nonce", true)
		return nil, "", false
	}
	user := string(userV)
	key := s.creds.keyFor(user)
	if key == nil {
		s.sendError(msg, from, 401, "Unauthorized", true)
		return nil, "", false
	}
	if !VerifyMessageIntegrity(raw, key) {
		s.sendError(msg, from, 401, "Unauthorized", true)
		return nil, "", false
	}
	return key, user, true
}

func (s *Server) handleAllocate(raw []byte, msg *Message, from *net.UDPAddr) {
	// Sem MI → desafio 401.
	if _, ok := msg.Get(AttrMessageIntegrity); !ok {
		s.sendError(msg, from, 401, "Unauthorized", true)
		return
	}
	key, user, ok := s.authenticate(raw, msg, from)
	if !ok {
		return
	}
	// REQUESTED-TRANSPORT obrigatório, deve ser UDP=17.
	rtV, hasRT := msg.Get(AttrRequestedTransport)
	if !hasRT || len(rtV) < 4 || rtV[0] != 17 {
		s.sendError(msg, from, 442, "Unsupported Transport Protocol", false)
		return
	}
	lifetime := defaultLifetime
	if lv, ok := msg.Get(AttrLifetime); ok {
		req := time.Duration(DecodeUint32(lv)) * time.Second
		if req > 0 && req < maxLifetime {
			lifetime = req
		}
	}
	if existing := s.allocs.get(from); existing != nil {
		s.sendError(msg, from, 437, "Allocation Mismatch", false)
		_ = existing
		return
	}
	a, err := s.allocs.create(from, user, key, lifetime, s)
	if err != nil {
		s.sendError(msg, from, 500, "Server Error", false)
		return
	}
	log.Printf("[turn] alloc client=%s relay=%s user=%s", from, a.relayAddr, user)

	resp := &Message{Type: msgType(methodAllocate, classSuccess), TransactionID: msg.TransactionID}
	resp.Add(AttrXORRelayedAddress, EncodeXORAddr(a.relayAddr, msg.TransactionID))
	resp.Add(AttrXORMappedAddress, EncodeXORAddr(from, msg.TransactionID))
	resp.Add(AttrLifetime, EncodeUint32(uint32(lifetime.Seconds())))
	resp.Add(AttrSoftware, []byte(softwareName))
	out := AppendMessageIntegrity(resp.Encode(), key)
	s.send(out, from)
}

func (s *Server) handleRefresh(raw []byte, msg *Message, from *net.UDPAddr) {
	a := s.allocs.get(from)
	if a == nil {
		s.sendError(msg, from, 437, "Allocation Mismatch", false)
		return
	}
	key, _, ok := s.authenticate(raw, msg, from)
	if !ok {
		return
	}
	lifetime := defaultLifetime
	if lv, ok := msg.Get(AttrLifetime); ok {
		req := time.Duration(DecodeUint32(lv)) * time.Second
		if req == 0 {
			s.allocs.remove(from)
			lifetime = 0
		} else if req < maxLifetime {
			lifetime = req
		}
	}
	if lifetime > 0 {
		a.refresh(lifetime)
	}
	resp := &Message{Type: msgType(methodRefresh, classSuccess), TransactionID: msg.TransactionID}
	resp.Add(AttrLifetime, EncodeUint32(uint32(lifetime.Seconds())))
	out := AppendMessageIntegrity(resp.Encode(), key)
	s.send(out, from)
}

func (s *Server) handleCreatePermission(raw []byte, msg *Message, from *net.UDPAddr) {
	a := s.allocs.get(from)
	if a == nil {
		s.sendError(msg, from, 437, "Allocation Mismatch", false)
		return
	}
	key, _, ok := s.authenticate(raw, msg, from)
	if !ok {
		return
	}
	added := 0
	for _, attr := range msg.Attributes {
		if attr.Type != AttrXORPeerAddress {
			continue
		}
		peer, err := DecodeXORAddr(attr.Value, msg.TransactionID)
		if err != nil {
			s.sendError(msg, from, 400, "Bad Request", false)
			return
		}
		a.addPermission(peer.IP)
		added++
	}
	if added == 0 {
		s.sendError(msg, from, 400, "Bad Request", false)
		return
	}
	resp := &Message{Type: msgType(methodCreatePermission, classSuccess), TransactionID: msg.TransactionID}
	out := AppendMessageIntegrity(resp.Encode(), key)
	s.send(out, from)
}

func (s *Server) handleChannelBind(raw []byte, msg *Message, from *net.UDPAddr) {
	a := s.allocs.get(from)
	if a == nil {
		s.sendError(msg, from, 437, "Allocation Mismatch", false)
		return
	}
	key, _, ok := s.authenticate(raw, msg, from)
	if !ok {
		return
	}
	chV, hasCh := msg.Get(AttrChannelNumber)
	peerV, hasPeer := msg.Get(AttrXORPeerAddress)
	if !hasCh || !hasPeer || len(chV) < 4 {
		s.sendError(msg, from, 400, "Bad Request", false)
		return
	}
	num := binary.BigEndian.Uint16(chV[0:2])
	if num < channelMin || num > channelMax {
		s.sendError(msg, from, 400, "Bad Request", false)
		return
	}
	peer, err := DecodeXORAddr(peerV, msg.TransactionID)
	if err != nil {
		s.sendError(msg, from, 400, "Bad Request", false)
		return
	}
	if err := a.bindChannel(num, peer); err != nil {
		s.sendError(msg, from, 400, "Bad Request", false)
		return
	}
	resp := &Message{Type: msgType(methodChannelBind, classSuccess), TransactionID: msg.TransactionID}
	out := AppendMessageIntegrity(resp.Encode(), key)
	s.send(out, from)
}

func (s *Server) handleSendIndication(msg *Message, from *net.UDPAddr) {
	a := s.allocs.get(from)
	if a == nil {
		return
	}
	peerV, hasPeer := msg.Get(AttrXORPeerAddress)
	dataV, hasData := msg.Get(AttrData)
	if !hasPeer || !hasData {
		return
	}
	peer, err := DecodeXORAddr(peerV, msg.TransactionID)
	if err != nil {
		return
	}
	if !a.hasPermission(peer.IP) {
		return
	}
	if _, err := a.relayConn.WriteToUDP(dataV, peer); err != nil {
		return
	}
	relayOut.Add(1)
}

func (s *Server) handleChannelData(raw []byte, from *net.UDPAddr) {
	num, data, ok := decodeChannelData(raw)
	if !ok {
		return
	}
	a := s.allocs.get(from)
	if a == nil {
		return
	}
	peer, ok := a.peerForChannel(num)
	if !ok {
		return
	}
	if _, err := a.relayConn.WriteToUDP(data, peer); err != nil {
		return
	}
	relayOut.Add(1)
}

// relayReadLoop lê pacotes do peer no socket relay e os entrega ao cliente
// TURN como Data indication ou ChannelData.
func (s *Server) relayReadLoop(a *Allocation) {
	buf := make([]byte, 65535)
	for {
		n, peer, err := a.relayConn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		relayIn.Add(1)
		if !a.hasPermission(peer.IP) {
			continue
		}
		data := buf[:n]
		if num, ok := a.channelFor(peer); ok {
			out := encodeChannelData(num, data)
			s.send(out, a.clientAddr)
			continue
		}
		// Data indication. Transaction ID novo aleatório.
		var tid [TIDSize]byte
		_, _ = randRead(tid[:])
		ind := &Message{Type: msgType(methodData, classIndication), TransactionID: tid}
		ind.Add(AttrXORPeerAddress, EncodeXORAddr(peer, tid))
		ind.Add(AttrData, data)
		s.send(ind.Encode(), a.clientAddr)
	}
}

func (s *Server) sendError(msg *Message, from *net.UDPAddr, code int, reason string, challenge bool) {
	method := methodOf(msg.Type)
	resp := &Message{Type: msgType(method, classError), TransactionID: msg.TransactionID}
	resp.Add(AttrErrorCode, EncodeErrorCode(code, reason))
	if challenge {
		resp.Add(AttrRealm, []byte(s.creds.realm))
		resp.Add(AttrNonce, []byte(s.creds.newNonce()))
	}
	resp.Add(AttrSoftware, []byte(softwareName))
	s.send(resp.Encode(), from)
}

func (s *Server) send(buf []byte, to *net.UDPAddr) {
	if _, err := s.conn.WriteToUDP(buf, to); err != nil {
		log.Printf("[turn] send %s: %v", to, err)
		return
	}
	pktOut.Add(1)
}

func (s *Server) gcLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.allocs.gc()
			log.Printf("[turn] stats in=%d out=%d relayIn=%d relayOut=%d",
				pktIn.Load(), pktOut.Load(), relayIn.Load(), relayOut.Load())
		}
	}
}

func shutdownOnSignal(cancel context.CancelFunc, conn *net.UDPConn) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	log.Printf("[turn] shutting down")
	cancel()
	_ = conn.Close()
}

// randRead embrulha crypto/rand sem retornar erro pra call site quente.
func randRead(b []byte) (int, error) {
	return cryptoReader.Read(b)
}
