// Parser de TWCC Feedback (RTPFB fmt=15).
//
// Espelho de twcc.go (que monta o FB). Aqui consumimos o FB que o
// subscriber manda PRA NÓS, pra alimentar o BWE downstream.
//
// Layout (após os 12 bytes header+SSRCs do RTPFB):
//
//	base_seq(16) | pkt_status_count(16) | reference_time(24 signed, 64ms)
//	| fb_pkt_count(8) | chunks... | recv_deltas... | padding
//
// Chunks: bit 15 = 0 → Run-Length (S=2 bits, length=13 bits) cobrindo `length`
// pacotes com o mesmo status; bit 15 = 1 → Status Vector com T=bit14:
//   T=0 → 14 símbolos de 1 bit (0=missing, 1=small delta)
//   T=1 → 7 símbolos de 2 bits (00 missing, 01 small, 10 large, 11 reserved)
package main

import (
	"encoding/binary"
	"errors"
)

// TWCCArrival = chegada (ou não) de um pacote identificado por twcc seq.
type TWCCArrival struct {
	Seq      uint16
	Received bool
	// ArrivalUS é absoluto (refTimeUS + soma dos deltas) só se Received.
	ArrivalUS int64
}

// ParseTWCCFeedback decodifica um pacote RTPFB fmt=15 já dividido por
// SplitCompound. Devolve a base seq, ref-time absoluta em microssegundos
// e a sequência de chegadas (na ordem de seq).
func ParseTWCCFeedback(p RTCPPacket) (baseSeq uint16, refTimeUS int64, arrivals []TWCCArrival, err error) {
	if !p.IsTransportCC() || len(p.Raw) < 20 {
		return 0, 0, nil, errors.New("twcc: not a transport-cc fb")
	}
	body := p.Raw[12:]
	baseSeq = binary.BigEndian.Uint16(body[0:2])
	count := int(binary.BigEndian.Uint16(body[2:4]))
	if count == 0 || count > 0xFFFE {
		return 0, 0, nil, errors.New("twcc: bad count")
	}
	// reference_time é signed 24-bit, unidade 64ms.
	rt := int32(uint32(body[4])<<16 | uint32(body[5])<<8 | uint32(body[6]))
	if rt&0x800000 != 0 {
		rt |= ^0xFFFFFF // sign-extend
	}
	refTimeUS = int64(rt) * 64 * 1000

	// fbCount := body[7] // ignorado
	off := 8
	symbols := make([]byte, 0, count)
	for len(symbols) < count {
		if off+2 > len(body) {
			return 0, 0, nil, errors.New("twcc: chunk truncated")
		}
		c := binary.BigEndian.Uint16(body[off : off+2])
		off += 2
		if c&0x8000 == 0 {
			// Run-length
			sym := byte((c >> 13) & 0x3)
			length := int(c & 0x1FFF)
			if length == 0 {
				return 0, 0, nil, errors.New("twcc: zero run length")
			}
			for i := 0; i < length && len(symbols) < count; i++ {
				symbols = append(symbols, sym)
			}
		} else if c&0x4000 == 0 {
			// Status vector T=0: 14 símbolos de 1 bit (0/1)
			for i := 0; i < 14 && len(symbols) < count; i++ {
				if c&(1<<uint(13-i)) != 0 {
					symbols = append(symbols, 1)
				} else {
					symbols = append(symbols, 0)
				}
			}
		} else {
			// Status vector T=1: 7 símbolos de 2 bits
			for i := 0; i < 7 && len(symbols) < count; i++ {
				sym := byte((c >> uint(12-i*2)) & 0x3)
				symbols = append(symbols, sym)
			}
		}
	}

	arrivals = make([]TWCCArrival, count)
	cur := refTimeUS
	for i, sym := range symbols {
		a := TWCCArrival{Seq: baseSeq + uint16(i)}
		switch sym {
		case 0, 3:
			a.Received = false
		case 1:
			if off+1 > len(body) {
				return 0, 0, nil, errors.New("twcc: small delta truncated")
			}
			d := int64(body[off]) * 250
			off++
			cur += d
			a.Received = true
			a.ArrivalUS = cur
		case 2:
			if off+2 > len(body) {
				return 0, 0, nil, errors.New("twcc: large delta truncated")
			}
			d := int64(int16(binary.BigEndian.Uint16(body[off:off+2]))) * 250
			off += 2
			cur += d
			a.Received = true
			a.ArrivalUS = cur
		}
		arrivals[i] = a
	}
	return baseSeq, refTimeUS, arrivals, nil
}
