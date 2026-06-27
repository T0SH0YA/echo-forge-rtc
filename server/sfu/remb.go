// REMB — Receiver Estimated Maximum Bitrate (draft-alvestrand-rmcat-remb).
// PSFB fmt=15, FCI prefixado por "REMB", seguido de NumSSRCs(1) +
// BR_exp(6) || BR_mantissa(18) (24 bits) + SSRCs.
//
// Etapa 10: estimativa simples — somamos os bitrates "alvos" das camadas
// que os subscribers querem daquele publisher e mandamos como REMB.
// É um floor: o publisher pode mandar menos, mas não mais.
package main

import "encoding/binary"

// BuildREMB monta um pacote REMB (PSFB fmt=15) anunciando bitrateBps
// pros SSRCs indicados.
func BuildREMB(senderSSRC uint32, bitrateBps uint64, ssrcs []uint32) []byte {
	n := len(ssrcs)
	totalLen := 16 + 4 + 4*n // header(4)+senderSSRC(4)+mediaSSRC(4)+"REMB"(4) + brword(4) + ssrcs
	out := make([]byte, totalLen)
	out[0] = 0x80 | FBFmtREMB // V=2, P=0, FMT=15
	out[1] = RTCPPSFB
	binary.BigEndian.PutUint16(out[2:4], uint16(totalLen/4)-1)
	binary.BigEndian.PutUint32(out[4:8], senderSSRC)
	binary.BigEndian.PutUint32(out[8:12], 0) // media SSRC unused (REMB usa lista abaixo)
	copy(out[12:16], "REMB")
	out[16] = byte(n)
	// Codifica bitrate em exp(6)+mantissa(18).
	exp := uint32(0)
	br := bitrateBps
	for br >= (1 << 18) {
		br >>= 1
		exp++
	}
	word := (exp << 18) | uint32(br&0x3FFFF)
	out[17] = byte(word >> 16)
	out[18] = byte(word >> 8)
	out[19] = byte(word)
	for i, s := range ssrcs {
		binary.BigEndian.PutUint32(out[20+4*i:24+4*i], s)
	}
	return out
}
