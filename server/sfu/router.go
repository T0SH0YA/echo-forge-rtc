// Router — forwarding 1→N de pacotes SRTP entre sessões da mesma sala.
//
// Modelo inicial (Etapa 7, single-room): toda sessão entra no Router quando
// DTLS estabelece. Quando chega um pacote SRTP de uma sessão, decifra com o
// contexto de recepção do publisher, depois re-cifra com o contexto de envio
// de cada outra sessão e manda pelo socket UDP principal pra cada remoteAddr.
//
// O SSRC e o SEQ são preservados — o subscriber vê o mesmo stream RTP que o
// publisher emitiu. Só muda o auth tag (porque a chave mudou).
//
// Salas múltiplas, simulcast e seleção de camada entram em etapas seguintes.
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
)

type Router struct {
	udp *net.UDPConn
	mu  sync.RWMutex
	ses map[string]*Session // sessionID → session
}

func NewRouter(udp *net.UDPConn) *Router {
	return &Router{udp: udp, ses: map[string]*Session{}}
}

func (r *Router) Add(s *Session) {
	r.mu.Lock()
	r.ses[s.ID] = s
	r.mu.Unlock()
}

func (r *Router) Remove(id string) {
	r.mu.Lock()
	delete(r.ses, id)
	r.mu.Unlock()
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
