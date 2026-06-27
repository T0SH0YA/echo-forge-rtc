// RTCP — parser e builder mínimos pra compound packets.
//
// RFC 3550 §6 (SR/RR/SDES/BYE), RFC 4585 (Feedback: RTPFB/PSFB),
// RFC 4585 §6.2.1 (NACK = RTPFB fmt=1), RFC 4585 §6.3.1 (PLI = PSFB fmt=1),
// draft-holmer-rmcat-transport-wide-cc-extensions (transport-cc = RTPFB fmt=15).
//
// Cada subpacote tem header de 4 bytes:
//
//	byte 0: V(2)=2  P(1)  RC/FMT(5)
//	byte 1: PT
//	byte 2-3: length (32-bit words MINUS 1)
//
// Pacotes de feedback (PT 205/206) têm o formato:
//
//	[ header(4) ][ SSRC do sender(4) ][ media SSRC(4) ][ FCI ... ]
package main

import (
	"encoding/binary"
	"errors"
)

const (
	RTCPSR     = 200
	RTCPRR     = 201
	RTCPSDES   = 202
	RTCPBYE    = 203
	RTCPAPP    = 204
	RTCPRTPFB  = 205 // generic RTP feedback (NACK, transport-cc)
	RTCPPSFB   = 206 // payload-specific feedback (PLI, FIR, REMB)

	FBFmtNACK        = 1
	FBFmtPLI         = 1
	FBFmtFIR         = 4
	FBFmtTransportCC = 15
	FBFmtREMB        = 15 // PSFB fmt=15 com "REMB" identifier
)

// RTCPPacket — view de um subpacote dentro de um compound RTCP.
type RTCPPacket struct {
	Version    byte
	Padding    bool
	Count      byte // RC ou FMT
	PayloadType byte
	SenderSSRC uint32 // bytes 4..7 (válido pra SR/RR/RTPFB/PSFB; SDES/BYE têm semântica diferente)
	MediaSSRC  uint32 // bytes 8..11 (só RTPFB/PSFB)
	Raw        []byte // pacote completo, incluindo header
}

// SplitCompound divide um buffer RTCP em N subpacotes contíguos.
func SplitCompound(buf []byte) ([]RTCPPacket, error) {
	out := []RTCPPacket{}
	off := 0
	for off < len(buf) {
		if len(buf)-off < 4 {
			return nil, errors.New("rtcp: short header")
		}
		ver := buf[off] >> 6
		if ver != 2 {
			return nil, errors.New("rtcp: bad version")
		}
		words := binary.BigEndian.Uint16(buf[off+2 : off+4])
		pktLen := int(words+1) * 4
		if off+pktLen > len(buf) {
			return nil, errors.New("rtcp: length overflow")
		}
		p := RTCPPacket{
			Version:     ver,
			Padding:     buf[off]&0x20 != 0,
			Count:       buf[off] & 0x1F,
			PayloadType: buf[off+1],
			Raw:         buf[off : off+pktLen],
		}
		if pktLen >= 8 {
			p.SenderSSRC = binary.BigEndian.Uint32(buf[off+4 : off+8])
		}
		if pktLen >= 12 && (p.PayloadType == RTCPRTPFB || p.PayloadType == RTCPPSFB) {
			p.MediaSSRC = binary.BigEndian.Uint32(buf[off+8 : off+12])
		}
		out = append(out, p)
		off += pktLen
	}
	return out, nil
}

// IsFeedback diz se vale roteamento upstream (subscriber → publisher).
func (p RTCPPacket) IsFeedback() bool {
	return p.PayloadType == RTCPRTPFB || p.PayloadType == RTCPPSFB
}

// IsPLI = PSFB com FMT=1
func (p RTCPPacket) IsPLI() bool { return p.PayloadType == RTCPPSFB && p.Count == FBFmtPLI }

// IsNACK = RTPFB com FMT=1
func (p RTCPPacket) IsNACK() bool { return p.PayloadType == RTCPRTPFB && p.Count == FBFmtNACK }

// IsTransportCC = RTPFB com FMT=15
func (p RTCPPacket) IsTransportCC() bool {
	return p.PayloadType == RTCPRTPFB && p.Count == FBFmtTransportCC
}

// RewriteSenderSSRC reescreve bytes 4..7 (sender SSRC) no buffer Raw.
// Necessário ao reencaminhar feedback: o "sender" do FB precisa ser o nosso
// SSRC local (o do SFU), não o do subscriber original.
func (p RTCPPacket) RewriteSenderSSRC(newSSRC uint32) {
	if len(p.Raw) < 8 {
		return
	}
	binary.BigEndian.PutUint32(p.Raw[4:8], newSSRC)
}

// BuildPLI constrói um pacote PSFB-PLI (12 bytes, sem FCI).
//   senderSSRC = nós; mediaSSRC = stream que queremos um keyframe.
func BuildPLI(senderSSRC, mediaSSRC uint32) []byte {
	out := make([]byte, 12)
	out[0] = 0x80 | FBFmtPLI // V=2, P=0, FMT=1
	out[1] = RTCPPSFB
	binary.BigEndian.PutUint16(out[2:4], 2) // length: (12/4)-1 = 2
	binary.BigEndian.PutUint32(out[4:8], senderSSRC)
	binary.BigEndian.PutUint32(out[8:12], mediaSSRC)
	return out
}
