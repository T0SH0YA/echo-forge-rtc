// BWE — estimativa de banda simples (loss-based AIMD).
//
// Etapa 13: olhamos a fração de perda observada no link publisher→SFU
// (computada a partir de gaps de seqüência RTP) e ajustamos uma estimativa
// alvo. O resultado vira REMB enviado periodicamente pro publisher como
// teto sugerido. Não é GCC — é um floor pragmático que evita o sender
// estourar a banda. GCC delay-based mais sofisticado entra numa etapa
// futura usando os deltas do TWCC.
//
// Algoritmo:
//   - taxa de perda ≤ 2%   → multiplicativo +8% (probe)
//   - taxa de perda 2..10% → mantém
//   - taxa de perda > 10%  → multiplicativo -15%
//   - clamp em [50 kbps, 8 Mbps]
//
// Janela: amostras acumuladas entre invocações de Tick(). Caller chama
// Tick() periodicamente (1Hz).
package main

import "sync"

const (
	bweMin       uint64 = 50_000
	bweMax       uint64 = 8_000_000
	bweInitial   uint64 = 500_000
	bweMinSample        = 30 // pacotes
)

type BWE struct {
	mu       sync.Mutex
	estimate uint64
	received uint64
	lost     uint64
}

func NewBWE() *BWE { return &BWE{estimate: bweInitial} }

func (b *BWE) OnReceived(n int) {
	if n <= 0 {
		return
	}
	b.mu.Lock()
	b.received += uint64(n)
	b.mu.Unlock()
}

func (b *BWE) OnLost(n int) {
	if n <= 0 {
		return
	}
	b.mu.Lock()
	b.lost += uint64(n)
	b.mu.Unlock()
}

// Tick recalcula e devolve a estimativa atual em bps.
func (b *BWE) Tick() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	total := b.received + b.lost
	if total < bweMinSample {
		return b.estimate
	}
	loss := float64(b.lost) / float64(total)
	switch {
	case loss > 0.10:
		b.estimate = uint64(float64(b.estimate) * 0.85)
	case loss < 0.02:
		b.estimate = uint64(float64(b.estimate) * 1.08)
	}
	if b.estimate < bweMin {
		b.estimate = bweMin
	}
	if b.estimate > bweMax {
		b.estimate = bweMax
	}
	b.received = 0
	b.lost = 0
	return b.estimate
}

// Estimate devolve o valor atual sem zerar contadores.
func (b *BWE) Estimate() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.estimate
}
