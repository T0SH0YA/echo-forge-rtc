// Router — forwarding 1→N de SRTP + roteamento de RTCP feedback (Etapa 8).
//
// Forward de mídia (subscriber recebe streams de todos os outros publishers):
//   - decifra com srtpRecv do publisher, re-cifra com srtpSend de cada
//     subscriber, preserva SSRC/SEQ.
//
// Forward de feedback (upstream: subscriber → publisher):
//   - PLI (PSFB/1), NACK (RTPFB/1), transport-cc (RTPFB/15) chegam com
//     mediaSSRC apontando o stream do publisher. Olhamos o owner do SSRC
//     e encaminhamos cifrado com a srtcpSend do publisher.
//
// RTX cache (re-transmissão local de pacotes em resposta a NACK sem ida ao
// publisher) ainda não — por enquanto delegamos pro publisher decidir.
package main

import (
	"context"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"
)


var (
	rtpIn   atomic.Uint64
	rtpFwd  atomic.Uint64
	rtpDrop atomic.Uint64
	rtcpIn  atomic.Uint64
	rtcpFwd atomic.Uint64
	rtcpFB  atomic.Uint64
	rtxHit  atomic.Uint64
	rtxMiss atomic.Uint64
)

type Router struct {
	udp *net.UDPConn

	mu   sync.RWMutex
	ses  map[string]*Session // sessionID → session
	ssrc map[uint32]*Session // mediaSSRC → publisher session
	rtx  *RTXCache
}

func NewRouter(udp *net.UDPConn) *Router {
	return &Router{udp: udp, ses: map[string]*Session{}, ssrc: map[uint32]*Session{}, rtx: NewRTXCache()}
}

func (r *Router) Add(s *Session) {
	r.mu.Lock()
	r.ses[s.ID] = s
	r.mu.Unlock()
}

func (r *Router) Remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.ses[id]; ok {
		delete(r.ses, id)
		for ssrc, owner := range r.ssrc {
			if owner == s {
				delete(r.ssrc, ssrc)
			}
		}
	}
}

// trackSSRC: chamado em todo RTP recebido — registra publisher do SSRC.
func (r *Router) trackSSRC(s *Session, ssrc uint32) {
	r.mu.RLock()
	cur, ok := r.ssrc[ssrc]
	r.mu.RUnlock()
	if ok && cur == s {
		return
	}
	r.mu.Lock()
	r.ssrc[ssrc] = s
	r.mu.Unlock()
	s.mu.Lock()
	if s.publishedSSRCs == nil {
		s.publishedSSRCs = map[uint32]bool{}
	}
	s.publishedSSRCs[ssrc] = true
	s.mu.Unlock()
}

// HandleRTP: pacote SRTP recebido de `from` (já demuxado pelo udpLoop).
func (r *Router) HandleRTP(from *Session, raw []byte) {
	rtpIn.Add(1)
	from.mu.Lock()
	recv := from.srtpRecv
	ridExtID := from.RIDExtID
	twccExtID := from.TWCCExtID
	twcc := from.twcc
	bwe := from.bwe
	from.mu.Unlock()
	if recv == nil {
		rtpDrop.Add(1)
		return
	}
	hdr, err := ParseRTP(raw)
	if err != nil {
		rtpDrop.Add(1)
		return
	}
	plain, err := recv.Decrypt(raw, hdr.HeaderLen, hdr.SSRC, hdr.SequenceNumber)
	if err != nil {
		rtpDrop.Add(1)
		log.Printf("[sfu] srtp decrypt fail ssrc=%d seq=%d err=%v", hdr.SSRC, hdr.SequenceNumber, err)
		return
	}
	r.trackSSRC(from, hdr.SSRC)

	// Simulcast: tenta extrair RID do header extension e registrar rid↔ssrc.
	if ridExtID > 0 && hdr.Extension && len(hdr.ExtensionData) > 0 {
		if v := ParseOneByteExt(hdr.ExtensionProfile, hdr.ExtensionData, ridExtID); len(v) > 0 {
			rid := string(v)
			if from.rememberLayer(rid, hdr.SSRC) {
				log.Printf("[sfu] simulcast layer discovered pub=%s rid=%s ssrc=%d", from.ID, rid, hdr.SSRC)
			}
		}
	}

	// Etapa 13: TWCC seq → recorder; gap por SSRC → BWE loss.
	if twccExtID > 0 && twcc != nil && hdr.Extension {
		if seq, ok := ParseTWCCSeq(hdr.ExtensionProfile, hdr.ExtensionData, twccExtID); ok {
			twcc.SetMediaSSRC(hdr.SSRC)
			twcc.Record(seq, time.Now().UnixMicro())
		}
	}
	if bwe != nil {
		from.mu.Lock()
		if from.lastSeq == nil {
			from.lastSeq = map[uint32]uint16{}
		}
		last, seen := from.lastSeq[hdr.SSRC]
		from.lastSeq[hdr.SSRC] = hdr.SequenceNumber
		from.mu.Unlock()
		bwe.OnReceived(1)
		if seen {
			gap := int16(hdr.SequenceNumber - last - 1)
			if gap > 0 && gap < 100 { // ignora reset/reordenamento grande
				bwe.OnLost(int(gap))
			}
		}
	}

	r.rtx.Put(hdr.SSRC, hdr.SequenceNumber, hdr.HeaderLen, plain)
	r.forward(from, plain, hdr.HeaderLen, hdr.SSRC, hdr.SequenceNumber)
}


// answerNACK reentrega localmente os pacotes pedidos via NACK, sem ida
// até o publisher. Retorna true se conseguiu servir todos (NACK consumido).
func (r *Router) answerNACK(from *Session, p RTCPPacket) bool {
	lost := ParseNACK(p)
	if len(lost) == 0 {
		return true
	}
	from.mu.Lock()
	send := from.srtpSend
	addr := from.remoteAddr
	from.mu.Unlock()
	if send == nil || addr == "" {
		return false
	}
	ua, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return false
	}
	served := 0
	for _, seq := range lost {
		headerLen, plain, ok := r.rtx.Get(p.MediaSSRC, seq)
		if !ok {
			rtxMiss.Add(1)
			continue
		}
		out, err := send.Encrypt(plain, headerLen, p.MediaSSRC, seq)
		if err != nil {
			continue
		}
		if _, err := r.udp.WriteToUDP(out, ua); err == nil {
			rtxHit.Add(1)
			served++
		}
	}
	return true
}

// shouldForward decide se o pacote do publisher `pub` (camada `layer`,
// `ssrc`) deve ir pro subscriber `sub`. Áudio e SSRCs sem layer descoberto
// passam sempre. Para vídeo simulcast, só passa a camada preferida (ou a
// mais alta disponível se o subscriber não escolheu).
func (r *Router) shouldForward(pub, sub *Session, ssrc uint32, layer string) bool {
	if layer == "" {
		return true // áudio / single-stream
	}
	pref := sub.getPrefLayer(pub.ID)
	if pref == "" {
		// fallback: maior camada disponível
		avail := pub.availableLayers()
		if len(avail) == 0 {
			return true
		}
		pref = avail[len(avail)-1]
	}
	return pref == layer
}

func (r *Router) forward(from *Session, plain []byte, headerLen int, ssrc uint32, seq uint16) {
	layer := from.layerOfSSRC(ssrc)

	r.mu.RLock()
	targets := make([]*Session, 0, len(r.ses))
	for _, s := range r.ses {
		if s != from {
			targets = append(targets, s)
		}
	}
	r.mu.RUnlock()

	for _, sub := range targets {
		if !r.shouldForward(from, sub, ssrc, layer) {
			continue
		}
		sub.mu.Lock()
		send := sub.srtpSend
		addr := sub.remoteAddr
		sub.mu.Unlock()
		if send == nil || addr == "" {
			continue
		}
		ua, err := net.ResolveUDPAddr("udp", addr)
		if err != nil {
			continue
		}
		out, err := send.Encrypt(plain, headerLen, ssrc, seq)
		if err != nil {
			continue
		}
		if _, err := r.udp.WriteToUDP(out, ua); err == nil {
			rtpFwd.Add(1)
		}
	}
}

// SwitchLayer: subscriber pede pra trocar a camada que recebe do publisher.
// Dispara PLI pro publisher pra acelerar a chegada de keyframe da nova
// camada. Retorna o SSRC alvo (0 se ainda não descoberto).
func (r *Router) SwitchLayer(subID, pubID, rid string) (uint32, error) {
	r.mu.RLock()
	sub := r.ses[subID]
	pub := r.ses[pubID]
	r.mu.RUnlock()
	if sub == nil {
		return 0, errSubNotFound
	}
	if pub == nil {
		return 0, errPubNotFound
	}
	sub.setPrefLayer(pubID, rid)

	pub.mu.Lock()
	targetSSRC := uint32(0)
	if pub.layerSSRC != nil {
		targetSSRC = pub.layerSSRC[rid]
	}
	srtcp := pub.srtcpSend
	addr := pub.remoteAddr
	pub.mu.Unlock()
	if targetSSRC != 0 && srtcp != nil && addr != "" {
		// Manda PLI cifrado pra acelerar keyframe da nova camada.
		pli := BuildPLI(0, targetSSRC)
		if cipher, err := srtcp.Encrypt(pli); err == nil {
			if ua, err := net.ResolveUDPAddr("udp", addr); err == nil {
				_, _ = r.udp.WriteToUDP(cipher, ua)
			}
		}
	}
	return targetSSRC, nil
}

var (
	errSubNotFound = fmtErr("subscriber not found")
	errPubNotFound = fmtErr("publisher not found")
)

type strErr string

func (e strErr) Error() string { return string(e) }
func fmtErr(s string) error    { return strErr(s) }


// HandleRTCP: decifra SRTCP do peer, parseia compound, encaminha feedback
// pro publisher dono do mediaSSRC.
func (r *Router) HandleRTCP(from *Session, raw []byte) {
	rtcpIn.Add(1)
	from.mu.Lock()
	recv := from.srtcpRecv
	from.mu.Unlock()
	if recv == nil {
		return
	}
	plain, err := recv.Decrypt(raw)
	if err != nil {
		log.Printf("[sfu] srtcp decrypt fail err=%v", err)
		return
	}
	pkts, err := SplitCompound(plain)
	if err != nil {
		return
	}
	// Agrupa feedback por owner pra enviar um compound por destino.
	// NACKs são CONSUMIDOS localmente via RTX cache; só PLI/transport-cc/etc.
	// sobem pro publisher.
	byOwner := map[*Session][]RTCPPacket{}
	for _, p := range pkts {
		if !p.IsFeedback() {
			continue
		}
		if p.IsNACK() {
			r.answerNACK(from, p)
			continue
		}
		r.mu.RLock()
		owner := r.ssrc[p.MediaSSRC]
		r.mu.RUnlock()
		if owner == nil || owner == from {
			continue
		}
		byOwner[owner] = append(byOwner[owner], p)
	}
	for owner, fbs := range byOwner {
		r.sendFeedback(owner, fbs)
	}
}

func (r *Router) sendFeedback(to *Session, pkts []RTCPPacket) {
	to.mu.Lock()
	send := to.srtcpSend
	addr := to.remoteAddr
	to.mu.Unlock()
	if send == nil || addr == "" {
		return
	}
	ua, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return
	}
	// Concatena os pacotes FB num compound. Sender SSRC fica como o do
	// remetente original — browsers aceitam FB com qualquer sender SSRC.
	plain := []byte{}
	for _, p := range pkts {
		plain = append(plain, p.Raw...)
	}
	cipher, err := send.Encrypt(plain)
	if err != nil {
		return
	}
	if _, err := r.udp.WriteToUDP(cipher, ua); err == nil {
		rtcpFwd.Add(1)
		rtcpFB.Add(uint64(len(pkts)))
	}
}
