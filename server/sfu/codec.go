// STUN codec (RFC 5389) — subset usado pelo agente ICE-lite do SFU.
// Inclui atributos ICE (RFC 5245/8445): PRIORITY, USE-CANDIDATE,
// ICE-CONTROLLED, ICE-CONTROLLING.
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
	MagicCookie    uint32 = 0x2112A442
	HeaderSize            = 20
	TIDSize               = 12
	fingerprintXOR uint32 = 0x5354554E
)

const (
	classRequest uint16 = 0x00
	classSuccess uint16 = 0x02
	classError   uint16 = 0x03

	methodBinding uint16 = 0x001
)

const (
	AttrUsername         uint16 = 0x0006
	AttrMessageIntegrity uint16 = 0x0008
	AttrErrorCode        uint16 = 0x0009
	AttrXORMappedAddress uint16 = 0x0020
	AttrPriority         uint16 = 0x0024
	AttrUseCandidate     uint16 = 0x0025
	AttrSoftware         uint16 = 0x8022
	AttrFingerprint      uint16 = 0x8028
	AttrIceControlled    uint16 = 0x8029
	AttrIceControlling   uint16 = 0x802A
)

const (
	familyIPv4 byte = 0x01
	familyIPv6 byte = 0x02
)

type Attribute struct {
	Type  uint16
	Value []byte
}

type Message struct {
	Type          uint16
	TransactionID [TIDSize]byte
	Attributes    []Attribute
}

func msgType(method, class uint16) uint16 {
	m := method & 0x0FFF
	m3 := m & 0x000F
	m6 := (m & 0x0070) << 1
	m11 := (m & 0x0F80) << 2
	c0 := (class & 0x1) << 4
	c1 := (class & 0x2) << 7
	return m11 | c1 | m6 | c0 | m3
}
func methodOf(t uint16) uint16 { return (t & 0x000F) | ((t & 0x00E0) >> 1) | ((t & 0x3E00) >> 2) }
func classOf(t uint16) uint16  { return ((t & 0x0010) >> 4) | ((t & 0x0100) >> 7) }

// IsSTUN: bits 0-1 são 00 E magic cookie 0x2112A442 nos bytes 4-7 (RFC 5389
// §6). O magic cookie é o único jeito confiável de distinguir STUN de DTLS
// (cujo handshake content type também tem bits 0-1 = 00).
func IsSTUN(b []byte) bool {
	if len(b) < 20 || b[0]&0xC0 != 0 {
		return false
	}
	return b[4] == 0x21 && b[5] == 0x12 && b[6] == 0xA4 && b[7] == 0x42
}

func Decode(b []byte) (*Message, error) {
	if len(b) < HeaderSize {
		return nil, errors.New("stun: short")
	}
	if b[0]&0xC0 != 0 {
		return nil, errors.New("stun: bad bits")
	}
	mt := binary.BigEndian.Uint16(b[0:2])
	mlen := binary.BigEndian.Uint16(b[2:4])
	if binary.BigEndian.Uint32(b[4:8]) != MagicCookie {
		return nil, errors.New("stun: bad cookie")
	}
	if int(mlen)+HeaderSize > len(b) || mlen%4 != 0 {
		return nil, fmt.Errorf("stun: bad length %d/%d", mlen, len(b))
	}
	m := &Message{Type: mt}
	copy(m.TransactionID[:], b[8:20])
	p := b[HeaderSize : HeaderSize+int(mlen)]
	for len(p) > 0 {
		if len(p) < 4 {
			return nil, errors.New("stun: short attr")
		}
		at := binary.BigEndian.Uint16(p[0:2])
		al := binary.BigEndian.Uint16(p[2:4])
		if int(al)+4 > len(p) {
			return nil, errors.New("stun: attr overflow")
		}
		v := make([]byte, al)
		copy(v, p[4:4+al])
		m.Attributes = append(m.Attributes, Attribute{at, v})
		pad := (4 - int(al)%4) % 4
		p = p[4+int(al)+pad:]
	}
	return m, nil
}

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
	pad := (4 - len(v)%4) % 4
	for i := 0; i < pad; i++ {
		buf = append(buf, 0)
	}
	return buf
}

func (m *Message) Add(t uint16, v []byte) { m.Attributes = append(m.Attributes, Attribute{t, v}) }
func (m *Message) Has(t uint16) bool      { _, ok := m.Get(t); return ok }
func (m *Message) Get(t uint16) ([]byte, bool) {
	for _, a := range m.Attributes {
		if a.Type == t {
			return a.Value, true
		}
	}
	return nil, false
}

func EncodeXORAddr(addr *net.UDPAddr, tid [TIDSize]byte) []byte {
	ip4 := addr.IP.To4()
	if ip4 != nil {
		v := make([]byte, 8)
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

// AppendMessageIntegrity: key = short-term para ICE (= remote/local ice-pwd em ASCII).
func AppendMessageIntegrity(raw, key []byte) []byte {
	totalLen := len(raw) - HeaderSize + 24
	tmp := make([]byte, len(raw))
	copy(tmp, raw)
	binary.BigEndian.PutUint16(tmp[2:4], uint16(totalLen))
	mac := hmac.New(sha1.New, key)
	mac.Write(tmp)
	return appendAttr(tmp, AttrMessageIntegrity, mac.Sum(nil))
}

// VerifyMessageIntegrity: MI pode ter FINGERPRINT depois (ICE sempre envia ambos).
func VerifyMessageIntegrity(raw, key []byte) bool {
	if len(raw) < HeaderSize+24 {
		return false
	}
	miStart := -1
	// MI é o último de tudo OU penúltimo (com FP de 8 bytes depois).
	candidates := []int{len(raw) - 24, len(raw) - 24 - 8}
	for _, c := range candidates {
		if c < HeaderSize {
			continue
		}
		if binary.BigEndian.Uint16(raw[c:c+2]) == AttrMessageIntegrity {
			miStart = c
			break
		}
	}
	if miStart < 0 {
		return false
	}
	pseudo := make([]byte, miStart)
	copy(pseudo, raw[:miStart])
	totalLen := miStart - HeaderSize + 24
	binary.BigEndian.PutUint16(pseudo[2:4], uint16(totalLen))
	mac := hmac.New(sha1.New, key)
	mac.Write(pseudo)
	return hmac.Equal(mac.Sum(nil), raw[miStart+4:miStart+24])
}

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

func EncodeErrorCode(code int, reason string) []byte {
	v := make([]byte, 4+len(reason))
	v[2] = byte(code/100) & 0x07
	v[3] = byte(code % 100)
	copy(v[4:], reason)
	return v
}
