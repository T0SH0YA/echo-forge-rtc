// Simulcast — descoberta de camadas (rid → ssrc) e seleção por subscriber.
//
// Quando o browser envia simulcast, todas as N camadas (Chrome: q/h/f, ou
// l/m/h) chegam no MESMO m-line BUNDLE, com SSRCs distintos e identificados
// pelo header extension `urn:ietf:params:rtp-hdrext:sdes:rtp-stream-id`
// (RID, RFC 8852). O SFU aprende o mapeamento (rid → ssrc) na primeira
// vez que vê cada SSRC com seu rid, e cada subscriber escolhe qual rid
// quer receber daquele publisher.
//
// Parsing do header extension: RFC 8285 one-byte form (profile 0xBEDE).
// Two-byte form (0x1000) é raro e ignoramos por enquanto.
package main

import (
	"encoding/binary"
	"strings"
)

// ParseOneByteExt percorre as extensões RFC 8285 one-byte (profile 0xBEDE)
// e devolve o valor da extensão com o ID pedido, ou nil se ausente.
//
// Layout de cada elemento: 1 byte ID(4)|L(4) + L+1 bytes de dados.
// ID=0 é padding (1 byte), ID=15 indica "stop processing".
func ParseOneByteExt(profile uint16, ext []byte, wantID uint8) []byte {
	if profile != 0xBEDE || wantID == 0 || wantID == 15 {
		return nil
	}
	off := 0
	for off < len(ext) {
		b := ext[off]
		if b == 0 { // padding
			off++
			continue
		}
		id := b >> 4
		length := int(b&0x0F) + 1
		off++
		if id == 15 {
			return nil
		}
		if off+length > len(ext) {
			return nil
		}
		if id == wantID {
			return ext[off : off+length]
		}
		off += length
	}
	return nil
}

// RIDExtURI — URIs negociadas em a=extmap pra identificar camada simulcast.
const (
	RIDExtURI    = "urn:ietf:params:rtp-hdrext:sdes:rtp-stream-id"
	RepairExtURI = "urn:ietf:params:rtp-hdrext:sdes:repaired-rtp-stream-id"
)

// LayerRank atribui ordem (0 = menor qualidade). Aceita convenções comuns
// do Chrome (q/h/f) e libwebrtc (l/m/h). Retorna -1 se desconhecido.
func LayerRank(rid string) int {
	switch strings.ToLower(rid) {
	case "q", "l", "low":
		return 0
	case "h", "m", "mid", "medium":
		return 1
	case "f", "hi", "high":
		return 2
	}
	return -1
}

// VP8IsKeyframe tenta detectar keyframe VP8 a partir do payload bruto
// (após o RTP header). Best-effort: o descriptor VP8 é variável; aqui
// pulamos o descriptor mínimo e checamos o bit P (frame type) do payload
// header VP8 — P=0 significa keyframe (RFC 7741 §4.3).
func VP8IsKeyframe(payload []byte) bool {
	if len(payload) < 1 {
		return false
	}
	off := 1 // byte 0 do descriptor
	x := payload[0]&0x80 != 0
	if x {
		if len(payload) < off+1 {
			return false
		}
		ext := payload[off]
		off++
		if ext&0x80 != 0 { // I bit: PictureID present
			if len(payload) < off+1 {
				return false
			}
			if payload[off]&0x80 != 0 { // M bit: 15-bit PictureID
				off += 2
			} else {
				off++
			}
		}
		if ext&0x40 != 0 { // L bit: TL0PICIDX
			off++
		}
		if ext&0x30 != 0 { // T or K bit: TID/KEYIDX
			off++
		}
	}
	s := payload[0]&0x10 != 0
	pid := payload[0] & 0x07
	if !s || pid != 0 {
		return false // não é início de partição 0
	}
	if len(payload) <= off {
		return false
	}
	// VP8 payload header byte 0: |Size0..2|H|VER..2|P|
	return payload[off]&0x01 == 0
}

// H264IsKeyframe — heurística pra NALU IDR/SPS. Cobre Single NAL, STAP-A
// e FU-A (start fragment).
func H264IsKeyframe(payload []byte) bool {
	if len(payload) < 1 {
		return false
	}
	nalu := payload[0] & 0x1F
	switch nalu {
	case 5, 7, 8: // IDR, SPS, PPS
		return true
	case 24: // STAP-A: percorre os NALs agregados
		off := 1
		for off+2 <= len(payload) {
			size := int(binary.BigEndian.Uint16(payload[off : off+2]))
			off += 2
			if off+size > len(payload) || size < 1 {
				break
			}
			inner := payload[off] & 0x1F
			if inner == 5 || inner == 7 || inner == 8 {
				return true
			}
			off += size
		}
	case 28, 29: // FU-A / FU-B
		if len(payload) >= 2 {
			start := payload[1]&0x80 != 0
			inner := payload[1] & 0x1F
			if start && (inner == 5 || inner == 7 || inner == 8) {
				return true
			}
		}
	}
	return false
}
