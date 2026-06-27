// Jitter Buffer — Etapa 15.
//
// Por (publisher session, SSRC), bufferiza brevemente pacotes RTP plaintext
// pra:
//   1) reordenar (entregar em ordem crescente de seq);
//   2) absorver jitter da rede (targetDelay ms);
//   3) detectar gaps e disparar NACK upstream pro publisher.
//
// Modelo: ring window de 512 slots indexado por seq16. Pacotes fora da
// janela ou já entregues são descartados. Uma goroutine de drenagem roda
// a cada `tickMs` ms e emite todo prefixo contíguo que já maturou (arrival
// + targetDelay ≤ now). Se o head do prefixo tiver gap maior que `nackGrace`
// segurando a fila, montamos NACK e pulamos pra frente.
//
// Comparações de seq são feitas via int16(a-b) pra lidar com wrap natural.
package main

import (
	"sync"
	"time"
)

const (
	jbWindowSize  = 512
	jbTickMs      = 5
	jbDefaultMs   = 30 // alvo de delay (média típica jitter LAN/WAN ok)
	jbMaxHoldMs   = 200 // se segurou mais que isso por gap, desiste e pula
	jbNackGraceMs = 20 // espera mínima antes de disparar NACK
)

type jbItem struct {
	seq     uint16
	hdr     *RTPHeader
	plain   []byte
	arrived time.Time
	present bool
}

// JitterBuffer roda por (publisher session, SSRC).
type JitterBuffer struct {
	ssrc        uint32
	targetDelay time.Duration
	maxHold     time.Duration
	nackGrace   time.Duration

	emit func(hdr *RTPHeader, plain []byte)
	nack func(ssrc uint32, lost []uint16)

	mu       sync.Mutex
	ring     [jbWindowSize]jbItem
	head     uint16 // próxima seq esperada
	primed   bool   // já recebemos o primeiro pacote
	lastNack map[uint16]time.Time // seq → último envio (rate-limit por seq)
	closed   bool
	stop     chan struct{}
}

func NewJitterBuffer(ssrc uint32, emit func(*RTPHeader, []byte), nack func(uint32, []uint16)) *JitterBuffer {
	jb := &JitterBuffer{
		ssrc:        ssrc,
		targetDelay: jbDefaultMs * time.Millisecond,
		maxHold:     jbMaxHoldMs * time.Millisecond,
		nackGrace:   jbNackGraceMs * time.Millisecond,
		emit:        emit,
		nack:        nack,
		lastNack:    map[uint16]time.Time{},
		stop:        make(chan struct{}),
	}
	go jb.loop()
	return jb
}

func (jb *JitterBuffer) Close() {
	jb.mu.Lock()
	if jb.closed {
		jb.mu.Unlock()
		return
	}
	jb.closed = true
	close(jb.stop)
	jb.mu.Unlock()
}

func seqLess(a, b uint16) bool { return int16(a-b) < 0 }

// Push insere um pacote plaintext recém-decifrado.
func (jb *JitterBuffer) Push(hdr *RTPHeader, plain []byte) {
	jb.mu.Lock()
	defer jb.mu.Unlock()
	if jb.closed {
		return
	}
	seq := hdr.SequenceNumber
	if !jb.primed {
		jb.head = seq
		jb.primed = true
	}
	// Já entregue ou muito antigo?
	diff := int16(seq - jb.head)
	if diff < 0 {
		jbLate.Add(1)
		return
	}
	if diff >= jbWindowSize {
		// Pacote muito à frente — assume reset/loss enorme: pula a janela.
		jb.flushAllLocked()
		jb.head = seq
		diff = 0
	}
	idx := seq % jbWindowSize
	slot := &jb.ring[idx]
	if slot.present && slot.seq == seq {
		jbDup.Add(1)
		return
	}
	*slot = jbItem{seq: seq, hdr: hdr, plain: plain, arrived: time.Now(), present: true}
	jbPush.Add(1)
}

// flushAllLocked descarta tudo. Caller segura jb.mu.
func (jb *JitterBuffer) flushAllLocked() {
	for i := range jb.ring {
		jb.ring[i] = jbItem{}
	}
}

func (jb *JitterBuffer) loop() {
	t := time.NewTicker(jbTickMs * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-jb.stop:
			return
		case <-t.C:
			jb.drain()
		}
	}
}

// drain emite todo prefixo contíguo maduro; lida com gaps via NACK ou pulo.
func (jb *JitterBuffer) drain() {
	now := time.Now()
	var toEmit []jbItem
	var lostSeqs []uint16

	jb.mu.Lock()
	for steps := 0; steps < jbWindowSize; steps++ {
		slot := &jb.ring[jb.head%jbWindowSize]
		if slot.present && slot.seq == jb.head {
			// Pacote presente — emite se maduro.
			if now.Sub(slot.arrived) < jb.targetDelay {
				break
			}
			toEmit = append(toEmit, *slot)
			*slot = jbItem{}
			jb.head++
			continue
		}
		// Gap: head ausente. Vê se alguém adiante já está segurando demais.
		oldest := jb.findOldestAheadLocked(now)
		if oldest == nil {
			break
		}
		held := now.Sub(oldest.arrived)
		if held < jb.nackGrace {
			break
		}
		// Junta seqs perdidos entre head e oldest.seq (exclusive).
		for s := jb.head; seqLess(s, oldest.seq); s++ {
			if !jb.recentNackedLocked(s, now) {
				lostSeqs = append(lostSeqs, s)
				jb.lastNack[s] = now
			}
			if len(lostSeqs) >= 64 {
				break
			}
		}
		if held >= jb.maxHold {
			// Desistiu de esperar: pula gap, head vai pro oldest.
			jb.head = oldest.seq
			jbSkip.Add(1)
			continue
		}
		break
	}
	// limpa lastNack velho
	for s, t := range jb.lastNack {
		if now.Sub(t) > 5*time.Second {
			delete(jb.lastNack, s)
		}
	}
	jb.mu.Unlock()

	for _, it := range toEmit {
		jbEmit.Add(1)
		jb.emit(it.hdr, it.plain)
	}
	if len(lostSeqs) > 0 && jb.nack != nil {
		jbNackOut.Add(1)
		jb.nack(jb.ssrc, lostSeqs)
	}
}

func (jb *JitterBuffer) recentNackedLocked(seq uint16, now time.Time) bool {
	t, ok := jb.lastNack[seq]
	if !ok {
		return false
	}
	return now.Sub(t) < 100*time.Millisecond
}

func (jb *JitterBuffer) findOldestAheadLocked(now time.Time) *jbItem {
	var oldest *jbItem
	for i := uint16(1); i < jbWindowSize; i++ {
		slot := &jb.ring[(jb.head+i)%jbWindowSize]
		if !slot.present {
			continue
		}
		if oldest == nil || slot.arrived.Before(oldest.arrived) {
			oldest = slot
		}
		_ = now
	}
	return oldest
}
