// Transport-Wide Congestion Control (TWCC) — draft-holmer-rmcat-transport-wide-cc-extensions-01.
//
// O publisher carimba CADA pacote RTP enviado com um número de sequência
// "transport-wide" de 16 bits, num header extension RFC 8285 one-byte (2 bytes).
// O receptor (nós, o SFU) registra (twcc_seq, arrival_us) e periodicamente
// devolve um pacote RTPFB fmt=15 listando os tempos de chegada — o sender
// usa isso pra estimar banda (delay-based GCC) e ajustar bitrate.
//
// Etapa 13: implementamos só o lado RX (recolher + montar FB). O sender
// (browser) já sabe consumir esses FBs. Em paralelo, um BWE simples
// loss-based (Bwe) sintetiza REMB pro publisher pra cravar um teto.
//
// Formato do FCI (após os 12 bytes de header RTCP+SSRCs):
//
//	base_seq(16) | pkt_status_count(16) | reference_time(24 signed,
//	 unidade 64ms) | fb_pkt_count(8) | chunks... | recv_deltas... | padding
//
// Usamos sempre Status Vector Chunks 2-bit (T=1,S=1): 7 símbolos/chunk.
// Símbolos: 00 não-recebido, 01 small delta (uint8, 250us), 10 large delta
// (int16, 250us), 11 reservado. Delta = arrival - last_arrival.
package main

import (
	"encoding/binary"
	"sort"
	"sync"
)

// URI negociada via a=extmap:<id> <uri>.
const TWCCExtURI = "http://www.ietf.org/id/draft-holmer-rmcat-transport-wide-cc-extensions-01"

// ParseTWCCSeq extrai o twcc seq (16-bit BE) do header extension one-byte.
func ParseTWCCSeq(profile uint16, ext []byte, extID uint8) (uint16, bool) {
	if extID == 0 {
		return 0, false
	}
	v := ParseOneByteExt(profile, ext, extID)
	if len(v) < 2 {
		return 0, false
	}
	return binary.BigEndian.Uint16(v[:2]), true
}

type twccSample struct {
	seq   uint16
	arrUS int64
}

// TWCCRecorder acumula chegadas observadas e gera FBs periódicos.
// Um por sessão receptora (publisher). senderSSRC = nosso SSRC arbitrário
// (browsers aceitam qualquer); mediaSSRC = um SSRC do publisher (qualquer
// do mesmo BUNDLE serve — TWCC é transport-wide, não per-stream).
type TWCCRecorder struct {
	mu         sync.Mutex
	samples    []twccSample
	fbCount    uint8
	senderSSRC uint32
	mediaSSRC  uint32
}

func NewTWCCRecorder(senderSSRC uint32) *TWCCRecorder {
	return &TWCCRecorder{senderSSRC: senderSSRC}
}

// SetMediaSSRC fixa o SSRC referência do publisher (o primeiro visto).
func (r *TWCCRecorder) SetMediaSSRC(ssrc uint32) {
	r.mu.Lock()
	if r.mediaSSRC == 0 {
		r.mediaSSRC = ssrc
	}
	r.mu.Unlock()
}

// Record adiciona um (twcc_seq, arrival_us).
func (r *TWCCRecorder) Record(seq uint16, arrUS int64) {
	r.mu.Lock()
	r.samples = append(r.samples, twccSample{seq, arrUS})
	r.mu.Unlock()
}

// Build drena as amostras pendentes e devolve um RTCP TWCC FB pronto
// (plaintext, antes de SRTCP). Retorna nil se nada a reportar.
//
// Caps em 256 pacotes por FB pra evitar pacotes > MTU; o restante fica
// pra próxima invocação.
func (r *TWCCRecorder) Build() []byte {
	r.mu.Lock()
	if len(r.samples) == 0 || r.mediaSSRC == 0 {
		r.mu.Unlock()
		return nil
	}
	max := 256
	var batch []twccSample
	if len(r.samples) > max {
		batch = append(batch, r.samples[:max]...)
		r.samples = append([]twccSample(nil), r.samples[max:]...)
	} else {
		batch = r.samples
		r.samples = nil
	}
	fbc := r.fbCount
	r.fbCount++
	sender := r.senderSSRC
	media := r.mediaSSRC
	r.mu.Unlock()

	// Ordena por seq (com wrap: usa janela curta, batch nunca cobre >32k).
	sort.Slice(batch, func(i, j int) bool {
		return int16(batch[i].seq-batch[j].seq) < 0
	})

	baseSeq := batch[0].seq
	maxSeq := batch[len(batch)-1].seq
	count := int(uint16(maxSeq-baseSeq)) + 1
	if count > 0xFFFE {
		count = 0xFFFE
	}

	recv := make(map[uint16]int64, len(batch))
	for _, s := range batch {
		recv[s.seq] = s.arrUS
	}

	refTime64 := (batch[0].arrUS / 1000) / 64 // múltiplo de 64ms
	refMS := refTime64 * 64
	prevUS := refMS * 1000

	symbols := make([]byte, count)
	deltas := []byte{}
	for i := 0; i < count; i++ {
		seq := baseSeq + uint16(i)
		a, ok := recv[seq]
		if !ok {
			symbols[i] = 0
			continue
		}
		d := (a - prevUS) / 250
		prevUS = a
		switch {
		case d >= 0 && d <= 0xFF:
			symbols[i] = 1
			deltas = append(deltas, byte(d))
		case d >= -32768 && d <= 32767:
			symbols[i] = 2
			deltas = append(deltas, byte(d>>8), byte(d))
		default:
			// fora da janela representável — marca como não-recebido
			symbols[i] = 0
		}
	}

	// Status Vector Chunks (T=1, S=1, 7 símbolos × 2 bits).
	chunks := []byte{}
	for i := 0; i < len(symbols); i += 7 {
		end := i + 7
		if end > len(symbols) {
			end = len(symbols)
		}
		var c uint16 = 0xC000
		for j := i; j < end; j++ {
			c |= uint16(symbols[j]&0x3) << uint(12-(j-i)*2)
		}
		chunks = append(chunks, byte(c>>8), byte(c))
	}

	// header(4) + sender(4) + media(4) + FCI fixo(8) + chunks + deltas.
	total := 12 + 8 + len(chunks) + len(deltas)
	pad := (4 - total%4) % 4
	total += pad
	out := make([]byte, total)
	out[0] = 0x80 | FBFmtTransportCC
	out[1] = RTCPRTPFB
	binary.BigEndian.PutUint16(out[2:4], uint16(total/4)-1)
	binary.BigEndian.PutUint32(out[4:8], sender)
	binary.BigEndian.PutUint32(out[8:12], media)
	binary.BigEndian.PutUint16(out[12:14], baseSeq)
	binary.BigEndian.PutUint16(out[14:16], uint16(count))
	rt := uint32(refTime64) & 0xFFFFFF
	out[16] = byte(rt >> 16)
	out[17] = byte(rt >> 8)
	out[18] = byte(rt)
	out[19] = fbc
	copy(out[20:20+len(chunks)], chunks)
	copy(out[20+len(chunks):], deltas)
	if pad > 0 {
		out[0] |= 0x20
		out[total-1] = byte(pad)
	}
	return out
}
