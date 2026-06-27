// Downstream BWE — delay-based GCC simplificado por subscriber.
//
// Modelo: pra cada pacote RTP enviado pro subscriber registramos
// (twcc_seq, sent_us, size_bits). Quando chega um TWCC FB do subscriber,
// pareamos com os chegados (arrival_us) e computamos:
//
//   - taxa de perda na janela do FB
//   - delay-gradient: média das diferenças (Δarrival − Δsent) entre pares
//     consecutivos de pacotes recebidos
//
// AIMD:
//   - delay-gradient > +overuseUS (default 10ms acumulados na janela) → -15%
//   - loss > 10%                                                       → -15%
//   - delay normal e loss < 2%                                        → +8%
//   - clamp [50 kbps, 10 Mbps], init 1 Mbps.
//
// Não é GCC completo (sem Kalman), mas captura o sinal: latência crescendo
// = fila acumulando = banda saturada.
package main

import (
	"sync"
)

const (
	subBweMin    uint64 = 50_000
	subBweMax    uint64 = 10_000_000
	subBweInit   uint64 = 1_000_000
	sendHistSize        = 1024
)

type sendRecord struct {
	seq      uint16
	sentUS   int64
	sizeBits int
	used     bool
}

// DownstreamBWE — um por subscriber.
type DownstreamBWE struct {
	mu       sync.Mutex
	estimate uint64
	hist     [sendHistSize]sendRecord
	// last loss/delay snapshot pra observabilidade
	lastLoss  float64
	lastDelay int64 // µs acumulado na janela
}

func NewDownstreamBWE() *DownstreamBWE {
	return &DownstreamBWE{estimate: subBweInit}
}

// RecordSent: chamado a cada pacote saindo pro subscriber.
func (b *DownstreamBWE) RecordSent(seq uint16, sentUS int64, sizeBits int) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.hist[seq%sendHistSize] = sendRecord{seq: seq, sentUS: sentUS, sizeBits: sizeBits, used: true}
	b.mu.Unlock()
}

// OnFeedback: alimenta o estimador com uma janela de chegadas.
func (b *DownstreamBWE) OnFeedback(arrivals []TWCCArrival) {
	if b == nil || len(arrivals) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	received := 0
	lost := 0
	var prevSent, prevRecv int64
	havePrev := false
	delaySum := int64(0)
	deltaCount := 0

	for _, a := range arrivals {
		rec := b.hist[a.Seq%sendHistSize]
		if !rec.used || rec.seq != a.Seq {
			continue
		}
		if !a.Received {
			lost++
			continue
		}
		received++
		if havePrev {
			dSent := rec.sentUS - prevSent
			dRecv := a.ArrivalUS - prevRecv
			if dSent > 0 && dSent < 1_000_000 { // ignora gaps > 1s
				delaySum += dRecv - dSent
				deltaCount++
			}
		}
		prevSent = rec.sentUS
		prevRecv = a.ArrivalUS
		havePrev = true
	}

	total := received + lost
	if total < 10 {
		return // amostra pequena demais
	}
	loss := float64(lost) / float64(total)
	b.lastLoss = loss
	b.lastDelay = delaySum

	overuse := delaySum > 10_000 // 10ms acumulado na janela
	switch {
	case overuse, loss > 0.10:
		b.estimate = uint64(float64(b.estimate) * 0.85)
	case loss < 0.02 && delaySum < 2_000:
		b.estimate = uint64(float64(b.estimate) * 1.08)
	}
	if b.estimate < subBweMin {
		b.estimate = subBweMin
	}
	if b.estimate > subBweMax {
		b.estimate = subBweMax
	}
}

func (b *DownstreamBWE) Estimate() uint64 {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.estimate
}

func (b *DownstreamBWE) Snapshot() (est uint64, loss float64, delayUS int64) {
	if b == nil {
		return 0, 0, 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.estimate, b.lastLoss, b.lastDelay
}

// PickLayer escolhe um rid de `layers` que cabe no `estimate`.
// Heurística pragmática (Chrome simulcast típico): q≈200k, h≈600k, f≈1.7M.
// Devolve "" se não houver match.
func PickLayer(estimate uint64, layers []string) string {
	if len(layers) == 0 {
		return ""
	}
	// layers vem ordenado low→high (availableLayers usa LayerRank).
	type lb struct {
		rid    string
		needed uint64
	}
	bands := make([]lb, 0, len(layers))
	for _, r := range layers {
		var need uint64
		switch LayerRank(r) {
		case 0:
			need = 200_000
		case 1:
			need = 600_000
		case 2:
			need = 1_700_000
		default:
			need = 500_000
		}
		bands = append(bands, lb{r, need})
	}
	// escolhe maior rid cujo `needed` ≤ estimate*0.9 (margem).
	budget := uint64(float64(estimate) * 0.9)
	best := bands[0].rid
	for _, b := range bands {
		if b.needed <= budget {
			best = b.rid
		}
	}
	return best
}
