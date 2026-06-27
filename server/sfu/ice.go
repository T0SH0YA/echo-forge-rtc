// Agente ICE-lite: o servidor não inicia checks, apenas responde Binding
// requests do peer. Quando o peer marca um par com USE-CANDIDATE, fixamos
// aquele endereço como remoto da sessão (RFC 8445 §7.3 simplificado pra
// ICE-lite, §6.1.3) e iniciamos o handshake DTLS (Etapa 6).
package main

import (
	"context"
	"log"
	"net"
)

// HandleBinding processa um Binding Request UDP. Retorna (resposta, sessão)
// ou (nil, nil) se rejeitar silenciosamente.
func (s *Server) HandleBinding(raw []byte, from *net.UDPAddr) ([]byte, *Session) {
	msg, err := Decode(raw)
	if err != nil {
		return nil, nil
	}
	if methodOf(msg.Type) != methodBinding || classOf(msg.Type) != classRequest {
		return nil, nil
	}
	userV, ok := msg.Get(AttrUsername)
	if !ok {
		return nil, nil
	}
	localUfrag, _, ok := splitUsername(string(userV))
	if !ok {
		return nil, nil
	}
	sess := s.sessions.ByLocalUfrag(localUfrag)
	if sess == nil {
		return nil, nil
	}
	if !VerifyMessageIntegrity(raw, []byte(sess.LocalPwd)) {
		log.Printf("[sfu] ice binding MI fail from=%s ufrag=%s", from, localUfrag)
		resp := &Message{Type: msgType(methodBinding, classError), TransactionID: msg.TransactionID}
		resp.Add(AttrErrorCode, EncodeErrorCode(401, "Unauthorized"))
		return AppendFingerprint(resp.Encode()), sess
	}
	sess.markChecking()

	useCand := msg.Has(AttrUseCandidate)

	resp := &Message{Type: msgType(methodBinding, classSuccess), TransactionID: msg.TransactionID}
	resp.Add(AttrXORMappedAddress, EncodeXORAddr(from, msg.TransactionID))
	resp.Add(AttrSoftware, []byte(softwareName))
	out := AppendMessageIntegrity(resp.Encode(), []byte(sess.LocalPwd))
	out = AppendFingerprint(out)

	if useCand {
		sess.markConnected(from.String())
		s.sessions.BindAddr(from.String(), sess)
		log.Printf("[sfu] ice connected session=%s peer=%s", sess.ID, from)
		s.maybeStartDTLS(sess, from)
	}
	return out, sess
}

// maybeStartDTLS dispara o handshake DTLS uma única vez por sessão (idempotente).
// O SFU é sempre servidor DTLS (anunciamos setup:passive no answer).
func (s *Server) maybeStartDTLS(sess *Session, peer *net.UDPAddr) {
	sess.mu.Lock()
	if sess.dtlsStarted || sess.LocalCert == nil {
		sess.mu.Unlock()
		return
	}
	sess.dtlsStarted = true
	pipe := newDTLSPacketConn(s.udp, peer)
	sess.dtlsPipe = pipe
	sess.dtlsState = DTLSHandshaking
	cert := sess.LocalCert
	remoteFP := sess.RemoteFinger
	sess.mu.Unlock()

	go func() {
		conn, keys, err := runDTLSServer(context.Background(), pipe, cert, remoteFP)
		if err != nil {
			sess.mu.Lock()
			sess.dtlsState = DTLSFailed
			sess.mu.Unlock()
			log.Printf("[sfu] dtls handshake fail session=%s err=%v", sess.ID, err)
			return
		}
		// Cria contextos SRTP+SRTCP (Etapas 7 e 8). Mesmas chaves dos dois
		// lados — RFC 7714 §9.
		recv, errR := NewSRTPContext(keys.ClientKey, keys.ClientSalt)
		send, errS := NewSRTPContext(keys.ServerKey, keys.ServerSalt)
		rcpRecv, errCR := NewSRTCPContext(keys.ClientKey, keys.ClientSalt)
		rcpSend, errCS := NewSRTCPContext(keys.ServerKey, keys.ServerSalt)
		sess.mu.Lock()
		sess.dtlsConn = conn
		sess.srtpKeys = keys
		sess.dtlsState = DTLSEstablished
		if errR == nil && errS == nil && errCR == nil && errCS == nil {
			sess.srtpRecv = recv
			sess.srtpSend = send
			sess.srtcpRecv = rcpRecv
			sess.srtcpSend = rcpSend
		}
		// Etapa 13: inicia TWCC recorder + BWE pra esse peer.
		sess.rtpSSRC = randUint32NonZero()
		sess.twcc = NewTWCCRecorder(sess.rtpSSRC)
		sess.bwe = NewBWE()
		sess.lastSeq = map[uint32]uint16{}
		sess.mu.Unlock()

		dtlsHS.Add(1)
		if errR != nil || errS != nil || errCR != nil || errCS != nil {
			log.Printf("[sfu] srtp/srtcp ctx init fail session=%s recv=%v send=%v rcpR=%v rcpS=%v (profile=0x%04x)",
				sess.ID, errR, errS, errCR, errCS, uint16(keys.Profile))
			return
		}
		if s.router != nil {
			s.router.Add(sess)
		}
		log.Printf("[sfu] dtls+srtp+srtcp ready session=%s srtp_profile=0x%04x", sess.ID, uint16(keys.Profile))
		// Etapa 11: levanta SCTP/DataChannels sobre o mesmo DTLS.
		s.startSCTP(sess)
	}()
}

