// RTX cache — ring buffer por (publisher SSRC) de pacotes RTP recentes em
// claro, pra responder NACK localmente sem precisar ir até o publisher.
//
// Por que local: NACK ida-e-volta até o publisher dobra a latência e gasta
// banda upstream do publisher. SFU guarda os últimos N pacotes que viu e
// reentrega ao subscriber que pediu.
//
// O que guardamos: o pacote RTP em CLARO (plaintext, depois do SRTP open).
// Quando o subscriber pede SEQ Y, achamos no cache, re-cifra com a srtpSend
// daquela sessão e mandamos. SSRC/SEQ preservados — pro browser do
// subscriber é só "pacote chegou tarde", o jitter buffer dedup naturalmente.
//
// Por SSRC, ring de tamanho fixo (default 512 pacotes ≈ 1s a 30fps + áudio).
// Lookup é O(1): SEQ% N como hint, validação por SEQ exato.
package main

import (
	"sync"
)

const rtxRingSize = 1024

type rtxEntry struct {
	used      bool
	seq       uint16
	headerLen int
	plain     []byte // RTP completo em claro (header + payload)
}

type rtxRing struct {
	mu  sync.Mutex
	buf [rtxRingSize]rtxEntry
}

func (r *rtxRing) put(seq uint16, headerLen int, plain []byte) {
	idx := int(seq) % rtxRingSize
	cp := make([]byte, len(plain))
	copy(cp, plain)
	r.mu.Lock()
	r.buf[idx] = rtxEntry{used: true, seq: seq, headerLen: headerLen, plain: cp}
	r.mu.Unlock()
}

func (r *rtxRing) get(seq uint16) (int, []byte, bool) {
	idx := int(seq) % rtxRingSize
	r.mu.Lock()
	e := r.buf[idx]
	r.mu.Unlock()
	if !e.used || e.seq != seq {
		return 0, nil, false
	}
	return e.headerLen, e.plain, true
}

// RTXCache: SSRC → ring buffer.
type RTXCache struct {
	mu    sync.RWMutex
	rings map[uint32]*rtxRing
}

func NewRTXCache() *RTXCache { return &RTXCache{rings: map[uint32]*rtxRing{}} }

func (c *RTXCache) Put(ssrc uint32, seq uint16, headerLen int, plain []byte) {
	c.mu.RLock()
	r, ok := c.rings[ssrc]
	c.mu.RUnlock()
	if !ok {
		c.mu.Lock()
		if r = c.rings[ssrc]; r == nil {
			r = &rtxRing{}
			c.rings[ssrc] = r
		}
		c.mu.Unlock()
	}
	r.put(seq, headerLen, plain)
}

func (c *RTXCache) Get(ssrc uint32, seq uint16) (int, []byte, bool) {
	c.mu.RLock()
	r, ok := c.rings[ssrc]
	c.mu.RUnlock()
	if !ok {
		return 0, nil, false
	}
	return r.get(seq)
}
