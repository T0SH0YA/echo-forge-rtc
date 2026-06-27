// Feedback loop: TWCC FBs (~50ms) + REMB (~1s) pra cada publisher conhecido.
//
// Roda numa goroutine ligada ao ciclo de vida do servidor.
package main

import (
	"context"
	"crypto/rand"
	"log"
	"net"
	"sync/atomic"
	"time"
)


var (
	twccSent atomic.Uint64
	rembSent atomic.Uint64
)

// StartFeedbackLoop dispara o loop em background. Cancelado via ctx.
func (r *Router) StartFeedbackLoop(ctx context.Context) {
	go r.feedbackLoop(ctx)
}

func (r *Router) feedbackLoop(ctx context.Context) {
	twccTick := time.NewTicker(50 * time.Millisecond)
	rembTick := time.NewTicker(1 * time.Second)
	defer twccTick.Stop()
	defer rembTick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-twccTick.C:
			r.flushTWCC()
		case <-rembTick.C:
			r.flushREMB()
		}
	}
}

func (r *Router) flushTWCC() {
	r.mu.RLock()
	sessions := make([]*Session, 0, len(r.ses))
	for _, s := range r.ses {
		sessions = append(sessions, s)
	}
	r.mu.RUnlock()
	for _, s := range sessions {
		s.mu.Lock()
		twcc := s.twcc
		srtcp := s.srtcpSend
		addr := s.remoteAddr
		s.mu.Unlock()
		if twcc == nil || srtcp == nil || addr == "" {
			continue
		}
		fb := twcc.Build()
		if fb == nil {
			continue
		}
		cipher, err := srtcp.Encrypt(fb)
		if err != nil {
			continue
		}
		ua, err := net.ResolveUDPAddr("udp", addr)
		if err != nil {
			continue
		}
		if _, err := r.udp.WriteToUDP(cipher, ua); err == nil {
			twccSent.Add(1)
		}
	}
}

func (r *Router) flushREMB() {
	r.mu.RLock()
	sessions := make([]*Session, 0, len(r.ses))
	for _, s := range r.ses {
		sessions = append(sessions, s)
	}
	r.mu.RUnlock()
	for _, s := range sessions {
		s.mu.Lock()
		bwe := s.bwe
		srtcp := s.srtcpSend
		addr := s.remoteAddr
		sender := s.rtpSSRC
		var ssrcs []uint32
		for ssrc := range s.publishedSSRCs {
			ssrcs = append(ssrcs, ssrc)
		}
		s.mu.Unlock()
		if bwe == nil || srtcp == nil || addr == "" || len(ssrcs) == 0 {
			continue
		}
		est := bwe.Tick()
		pkt := BuildREMB(sender, est, ssrcs)
		cipher, err := srtcp.Encrypt(pkt)
		if err != nil {
			continue
		}
		ua, err := net.ResolveUDPAddr("udp", addr)
		if err != nil {
			continue
		}
		if _, err := r.udp.WriteToUDP(cipher, ua); err == nil {
			rembSent.Add(1)
			log.Printf("[sfu] remb session=%s est=%dkbps ssrcs=%d", s.ID, est/1000, len(ssrcs))
		}
	}
}

// randUint32NonZero — SSRC arbitrário pra remetente de FB/REMB.
func randUint32NonZero() uint32 {
	var b [4]byte
	for {
		_, _ = rand.Read(b[:])
		v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
		if v != 0 {
			return v
		}
	}
}
