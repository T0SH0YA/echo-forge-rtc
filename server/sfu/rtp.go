// RTP/RTCP — parser mínimo + demux RFC 7983.
//
// Não precisamos serializar RTP do zero: o SFU recebe pacotes do publisher e
// reencaminha o MESMO header pros subscribers, só trocando o auth tag SRTP
// (porque as chaves mudam por sessão). Por isso o parser só calcula o
// header length pra separar header (AAD) de payload (plaintext).
package main

import (
	"encoding/binary"
	"errors"
)

type RTPHeader struct {
	Version          byte
	Padding          bool
	Extension        bool
	CSRCCount        byte
	Marker           bool
	PayloadType      byte
	SequenceNumber   uint16
	Timestamp        uint32
	SSRC             uint32
	CSRC             []uint32
	ExtensionProfile uint16
	ExtensionData    []byte
	HeaderLen        int // bytes incluindo CSRCs + extensão
}

func ParseRTP(b []byte) (*RTPHeader, error) {
	if len(b) < 12 {
		return nil, errors.New("rtp: short")
	}
	h := &RTPHeader{
		Version:        b[0] >> 6,
		Padding:        b[0]&0x20 != 0,
		Extension:      b[0]&0x10 != 0,
		CSRCCount:      b[0] & 0x0F,
		Marker:         b[1]&0x80 != 0,
		PayloadType:    b[1] & 0x7F,
		SequenceNumber: binary.BigEndian.Uint16(b[2:4]),
		Timestamp:      binary.BigEndian.Uint32(b[4:8]),
		SSRC:           binary.BigEndian.Uint32(b[8:12]),
	}
	if h.Version != 2 {
		return nil, errors.New("rtp: bad version")
	}
	off := 12
	for i := byte(0); i < h.CSRCCount; i++ {
		if len(b) < off+4 {
			return nil, errors.New("rtp: csrc short")
		}
		h.CSRC = append(h.CSRC, binary.BigEndian.Uint32(b[off:off+4]))
		off += 4
	}
	if h.Extension {
		if len(b) < off+4 {
			return nil, errors.New("rtp: ext short")
		}
		h.ExtensionProfile = binary.BigEndian.Uint16(b[off : off+2])
		extWords := binary.BigEndian.Uint16(b[off+2 : off+4])
		extLen := int(extWords) * 4
		if len(b) < off+4+extLen {
			return nil, errors.New("rtp: ext payload short")
		}
		h.ExtensionData = b[off+4 : off+4+extLen]
		off += 4 + extLen
	}
	h.HeaderLen = off
	return h, nil
}

// IsRTPOrRTCP — RFC 7983: primeiro byte em [128,191] (V=2 RTP/RTCP).
func IsRTPOrRTCP(b []byte) bool {
	if len(b) < 2 {
		return false
	}
	return b[0] >= 128 && b[0] <= 191
}

// IsRTCP separa RTP de RTCP quando rtcp-mux: PT em [64,95] = RTCP
// (RFC 5761 §4 — PTs RTP nunca caem nessa faixa quando mux está em vigor).
func IsRTCP(b []byte) bool {
	if !IsRTPOrRTCP(b) {
		return false
	}
	pt := b[1] & 0x7F
	return pt >= 64 && pt <= 95
}
