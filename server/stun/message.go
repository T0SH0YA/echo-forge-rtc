// Package main — STUN RFC 5389. Implementação do zero, sem dependências.
//
// Layout (RFC 5389 §6):
//
//	 0                   1                   2                   3
//	 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
//	|0 0|     STUN Message Type     |         Message Length        |
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
//	|                         Magic Cookie                          |
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
//	|                                                               |
//	|                     Transaction ID (96 bits)                  |
//	|                                                               |
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
package main

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"net"
)

const (
	MagicCookie  uint32 = 0x2112A442
	HeaderSize          = 20
	TIDSize             = 12
	fingerprintXOR uint32 = 0x5354554E
)

// Message types (class << 4 + method bits).
const (
	BindingRequest        uint16 = 0x0001
	BindingSuccess        uint16 = 0x0101
	BindingErrorResponse  uint16 = 0x0111
	BindingIndication     uint16 = 0x0011
)

// Attribute types (RFC 5389 §18.2).
const (
	AttrMappedAddress     uint16 = 0x0001
	AttrUsername          uint16 = 0x0006
	AttrMessageIntegrity  uint16 = 0x0008
	AttrErrorCode         uint16 = 0x0009
	AttrUnknownAttributes uint16 = 0x000A
	AttrRealm             uint16 = 0x0014
	AttrNonce             uint16 = 0x0015
	AttrXORMappedAddress  uint16 = 0x0020
	AttrSoftware          uint16 = 0x8022
	AttrAlternateServer   uint16 = 0x8023
	AttrFingerprint       uint16 = 0x8028
)

// Address families.
const (
	familyIPv4 byte = 0x01
	familyIPv6 byte = 0x02
)

// Attribute é um par TLV.
type Attribute struct {
	Type  uint16
	Value []byte
}

// Message é um frame STUN parseado.
type Message struct {
	Type          uint16
	TransactionID [TIDSize]byte
	Attributes    []Attribute
}

// Decode interpreta b como mensagem STUN. Valida magic cookie e tamanhos.
func Decode(b []byte) (*Message, error) {
	if len(b) < HeaderSize {
		return nil, errors.New("stun: short header")
	}
	// Os dois primeiros bits devem ser zero (RFC 5389 §6).
	if b[0]&0xC0 != 0 {
		return nil, errors.New("stun: invalid leading bits")
	}
	mt := binary.BigEndian.Uint16(b[0:2])
	mlen := binary.BigEndian.Uint16(b[2:4])
	cookie := binary.BigEndian.Uint32(b[4:8])
	if cookie != MagicCookie {
		return nil, errors.New("stun: bad magic cookie")
	}
	if int(mlen)+HeaderSize != len(b) {
		return nil, fmt.Errorf("stun: length mismatch (declared=%d, actual=%d)", mlen, len(b)-HeaderSize)
	}
	if mlen%4 != 0 {
		return nil, errors.New("stun: length not multiple of 4")
	}

	m := &Message{Type: mt}
	copy(m.TransactionID[:], b[8:20])

	// Parse atributos. Cada um: type(2) len(2) value(len) + padding até múltiplo de 4.
	p := b[HeaderSize:]
	for len(p) > 0 {
		if len(p) < 4 {
			return nil, errors.New("stun: short attribute header")
		}
		at := binary.BigEndian.Uint16(p[0:2])
		al := binary.BigEndian.Uint16(p[2:4])
		if int(al)+4 > len(p) {
			return nil, errors.New("stun: attribute overflows message")
		}
		val := make([]byte, al)
		copy(val, p[4:4+al])
		m.Attributes = append(m.Attributes, Attribute{Type: at, Value: val})
		// Padding até 4 bytes.
		pad := (4 - int(al)%4) % 4
		p = p[4+int(al)+pad:]
	}
	return m, nil
}

// Encode serializa a mensagem (sem MESSAGE-INTEGRITY/FINGERPRINT automáticos —
// chame AppendMessageIntegrity / AppendFingerprint antes se quiser).
func (m *Message) Encode() []byte {
	body := make([]byte, 0, 64)
	for _, a := range m.Attributes {
		body = appendAttr(body, a.Type, a.Value)
	}
	out := make([]byte, HeaderSize+len(body))
	binary.BigEndian.PutUint16(out[0:2], m.Type)
	binary.BigEndian.PutUint16(out[2:4], uint16(len(body)))
	binary.BigEndian.PutUint32(out[4:8], MagicCookie)
	copy(out[8:20], m.TransactionID[:])
	copy(out[20:], body)
	return out
}

func appendAttr(buf []byte, t uint16, v []byte) []byte {
	hdr := [4]byte{}
	binary.BigEndian.PutUint16(hdr[0:2], t)
	binary.BigEndian.PutUint16(hdr[2:4], uint16(len(v)))
	buf = append(buf, hdr[:]...)
	buf = append(buf, v...)
	// Padding.
	pad := (4 - len(v)%4) % 4
	for i := 0; i < pad; i++ {
		buf = append(buf, 0)
	}
	return buf
}

// AddAttribute adiciona um atributo cru.
func (m *Message) AddAttribute(t uint16, v []byte) {
	m.Attributes = append(m.Attributes, Attribute{Type: t, Value: v})
}

// Get retorna o primeiro atributo do tipo dado.
func (m *Message) Get(t uint16) ([]byte, bool) {
	for _, a := range m.Attributes {
		if a.Type == t {
			return a.Value, true
		}
	}
	return nil, false
}

// --- XOR-MAPPED-ADDRESS (RFC 5389 §15.2) ---------------------------------

// EncodeXORMappedAddress serializa o atributo. O X-Port faz XOR com os 16 bits
// altos do magic cookie; o X-Address faz XOR com o cookie inteiro (IPv4) ou
// cookie || transactionID (IPv6).
func EncodeXORMappedAddress(addr *net.UDPAddr, tid [TIDSize]byte) []byte {
	ip4 := addr.IP.To4()
	if ip4 != nil {
		v := make([]byte, 8)
		v[0] = 0 // reserved
		v[1] = familyIPv4
		binary.BigEndian.PutUint16(v[2:4], uint16(addr.Port)^uint16(MagicCookie>>16))
		cookieBE := make([]byte, 4)
		binary.BigEndian.PutUint32(cookieBE, MagicCookie)
		for i := 0; i < 4; i++ {
			v[4+i] = ip4[i] ^ cookieBE[i]
		}
		return v
	}
	ip6 := addr.IP.To16()
	v := make([]byte, 20)
	v[0] = 0
	v[1] = familyIPv6
	binary.BigEndian.PutUint16(v[2:4], uint16(addr.Port)^uint16(MagicCookie>>16))
	mask := make([]byte, 16)
	binary.BigEndian.PutUint32(mask[0:4], MagicCookie)
	copy(mask[4:], tid[:])
	for i := 0; i < 16; i++ {
		v[4+i] = ip6[i] ^ mask[i]
	}
	return v
}

// DecodeXORMappedAddress reverte o XOR e devolve IP/porta.
func DecodeXORMappedAddress(v []byte, tid [TIDSize]byte) (*net.UDPAddr, error) {
	if len(v) < 8 {
		return nil, errors.New("stun: short xor-mapped-address")
	}
	fam := v[1]
	xport := binary.BigEndian.Uint16(v[2:4])
	port := int(xport ^ uint16(MagicCookie>>16))
	switch fam {
	case familyIPv4:
		if len(v) < 8 {
			return nil, errors.New("stun: short ipv4 xor-mapped-address")
		}
		cookieBE := make([]byte, 4)
		binary.BigEndian.PutUint32(cookieBE, MagicCookie)
		ip := make(net.IP, 4)
		for i := 0; i < 4; i++ {
			ip[i] = v[4+i] ^ cookieBE[i]
		}
		return &net.UDPAddr{IP: ip, Port: port}, nil
	case familyIPv6:
		if len(v) < 20 {
			return nil, errors.New("stun: short ipv6 xor-mapped-address")
		}
		mask := make([]byte, 16)
		binary.BigEndian.PutUint32(mask[0:4], MagicCookie)
		copy(mask[4:], tid[:])
		ip := make(net.IP, 16)
		for i := 0; i < 16; i++ {
			ip[i] = v[4+i] ^ mask[i]
		}
		return &net.UDPAddr{IP: ip, Port: port}, nil
	default:
		return nil, fmt.Errorf("stun: unknown family 0x%02x", fam)
	}
}

// --- MESSAGE-INTEGRITY (RFC 5389 §15.4) ----------------------------------

// AppendMessageIntegrity calcula HMAC-SHA1(key, mensagem-até-aqui) e anexa
// como atributo final. Reescreve o length do header pra refletir o atributo
// (length cobre os 24 bytes do MI já presentes, conforme RFC).
func AppendMessageIntegrity(raw []byte, key []byte) []byte {
	// Length que vai aparecer no header deve incluir o MI completo (24 bytes:
	// 4 header + 20 valor).
	totalLen := len(raw) - HeaderSize + 24
	tmp := make([]byte, len(raw))
	copy(tmp, raw)
	binary.BigEndian.PutUint16(tmp[2:4], uint16(totalLen))

	mac := hmac.New(sha1.New, key)
	mac.Write(tmp)
	sum := mac.Sum(nil)

	out := tmp
	out = appendAttr(out, AttrMessageIntegrity, sum)
	return out
}

// VerifyMessageIntegrity checa o último atributo MESSAGE-INTEGRITY em raw.
func VerifyMessageIntegrity(raw []byte, key []byte) bool {
	if len(raw) < HeaderSize+24 {
		return false
	}
	// Localiza o MI: deve ser os últimos 24 bytes (sem FINGERPRINT depois).
	miStart := len(raw) - 24
	if binary.BigEndian.Uint16(raw[miStart:miStart+2]) != AttrMessageIntegrity {
		// Pode estar antes de um FINGERPRINT (4+4=8 bytes finais).
		if len(raw) >= HeaderSize+24+8 {
			alt := len(raw) - 24 - 8
			if binary.BigEndian.Uint16(raw[alt:alt+2]) == AttrMessageIntegrity {
				miStart = alt
			} else {
				return false
			}
		} else {
			return false
		}
	}
	// Reescreve length cobrindo até fim do MI.
	pseudo := make([]byte, miStart)
	copy(pseudo, raw[:miStart])
	totalLen := miStart - HeaderSize + 24
	binary.BigEndian.PutUint16(pseudo[2:4], uint16(totalLen))
	mac := hmac.New(sha1.New, key)
	mac.Write(pseudo)
	expected := mac.Sum(nil)
	return hmac.Equal(expected, raw[miStart+4:miStart+24])
}

// --- FINGERPRINT (RFC 5389 §15.5) ----------------------------------------

// AppendFingerprint calcula CRC-32 do conteúdo até aqui (com length já incluindo
// o FINGERPRINT de 8 bytes) e anexa o atributo.
func AppendFingerprint(raw []byte) []byte {
	totalLen := len(raw) - HeaderSize + 8
	tmp := make([]byte, len(raw))
	copy(tmp, raw)
	binary.BigEndian.PutUint16(tmp[2:4], uint16(totalLen))
	crc := crc32.ChecksumIEEE(tmp) ^ fingerprintXOR
	v := make([]byte, 4)
	binary.BigEndian.PutUint32(v, crc)
	return appendAttr(tmp, AttrFingerprint, v)
}

// VerifyFingerprint checa o último atributo FINGERPRINT em raw.
func VerifyFingerprint(raw []byte) bool {
	if len(raw) < HeaderSize+8 {
		return false
	}
	fpStart := len(raw) - 8
	if binary.BigEndian.Uint16(raw[fpStart:fpStart+2]) != AttrFingerprint {
		return false
	}
	expected := binary.BigEndian.Uint32(raw[fpStart+4 : fpStart+8])
	crc := crc32.ChecksumIEEE(raw[:fpStart]) ^ fingerprintXOR
	return crc == expected
}
