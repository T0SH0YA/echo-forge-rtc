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
	"log"
	"net"
	"sync"
	"sync/atomic"
)

var (
	rtpIn   atomic.Uint64
	rtpFwd  atomic.Uint64
	rtpDrop atomic.Uint64
	rtcpIn  atomic.Uint64
	rtcpFwd atomic.Uint64
	rtcpFB  atomic.Uint64
)

type Router struct {
	udp *net.UDPConn

	mu   sync.RWMutex
	ses  map[string]*Session // sessionID → session
	ssrc map[uint32]*Session // mediaSSRC → publisher session
}

func NewRouter(udp *net.UDPConn) *Router {
	return &Router{udp: udp, ses: map[string]*Session{}, ssrc: map[uint32]*Session{}}
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
	r.forward(from, plain, hdr.HeaderLen, hdr.SSRC, hdr.SequenceNumber)
}

func (r *Router) forward(from *Session, plain []byte, headerLen int, ssrc uint32, seq uint16) {
	r.mu.RLock()
	targets := make([]*Session, 0, len(r.ses))
	for _, s := range r.ses {
		if s != from {
			targets = append(targets, s)
		}
	}
	r.mu.RUnlock()

	for _, sub := range targets {
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
	byOwner := map[*Session][]RTCPPacket{}
	for _, p := range pkts {
		if !p.IsFeedback() {
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
