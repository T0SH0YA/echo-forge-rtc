// Codec STUN (RFC 5389) + extensões TURN (RFC 5766/8656).
// Duplicado do server/stun de propósito: cada server é um módulo isolado.
package main

import (
	"crypto/hmac"
	"crypto/md5"
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

// Classes STUN (2 bits).
const (
	classRequest    uint16 = 0x00
	classIndication uint16 = 0x01
	classSuccess    uint16 = 0x02
	classError      uint16 = 0x03
)

// Métodos TURN (RFC 5766 §13) — também o método Binding (RFC 5389).
const (
	methodBinding          uint16 = 0x001
	methodAllocate         uint16 = 0x003
	methodRefresh          uint16 = 0x004
	methodSend             uint16 = 0x006
	methodData             uint16 = 0x007
	methodCreatePermission uint16 = 0x008
	methodChannelBind      uint16 = 0x009
)

// msgType monta o campo Type a partir de método (≤0x0FFF) + classe.
// Layout: 00 + M11..M7 + C1 + M6..M4 + C0 + M3..M0.
func msgType(method, class uint16) uint16 {
	m := method & 0x0FFF
	m3 := m & 0x000F
	m6 := (m & 0x0070) << 1
	m11 := (m & 0x0F80) << 2
	c0 := (class & 0x1) << 4
	c1 := (class & 0x2) << 7
	return m11 | c1 | m6 | c0 | m3
}

func methodOf(t uint16) uint16 {
	return (t & 0x000F) | ((t & 0x00E0) >> 1) | ((t & 0x3E00) >> 2)
}
func classOf(t uint16) uint16 {
	return ((t & 0x0010) >> 4) | ((t & 0x0100) >> 7)
}

// Atributos (RFC 5389 §18.2 + RFC 5766 §14).
const (
	AttrMappedAddress     uint16 = 0x0001
	AttrUsername          uint16 = 0x0006
	AttrMessageIntegrity  uint16 = 0x0008
	AttrErrorCode         uint16 = 0x0009
	AttrUnknownAttributes uint16 = 0x000A
	AttrChannelNumber     uint16 = 0x000C
	AttrLifetime          uint16 = 0x000D
	AttrXORPeerAddress    uint16 = 0x0012
	AttrData              uint16 = 0x0013
	AttrRealm             uint16 = 0x0014
	AttrNonce             uint16 = 0x0015
	AttrXORRelayedAddress uint16 = 0x0016
	AttrRequestedTransport uint16 = 0x0019
	AttrDontFragment      uint16 = 0x001A
	AttrXORMappedAddress  uint16 = 0x0020
	AttrSoftware          uint16 = 0x8022
	AttrFingerprint       uint16 = 0x8028
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

// IsChannelData diz se o primeiro byte indica ChannelData (00 = STUN, 01 = ChannelData).
func IsChannelData(b []byte) bool {
	return len(b) >= 4 && b[0]&0xC0 == 0x40
}

func Decode(b []byte) (*Message, error) {
	if len(b) < HeaderSize {
		return nil, errors.New("stun: short header")
	}
	if b[0]&0xC0 != 0 {
		return nil, errors.New("stun: invalid leading bits")
	}
	mt := binary.BigEndian.Uint16(b[0:2])
	mlen := binary.BigEndian.Uint16(b[2:4])
	cookie := binary.BigEndian.Uint32(b[4:8])
	if cookie != MagicCookie {
		return nil, errors.New("stun: bad magic cookie")
	}
	if int(mlen)+HeaderSize > len(b) {
		return nil, fmt.Errorf("stun: length mismatch declared=%d actual=%d", mlen, len(b)-HeaderSize)
	}
	if mlen%4 != 0 {
		return nil, errors.New("stun: length not multiple of 4")
	}
	m := &Message{Type: mt}
	copy(m.TransactionID[:], b[8:20])
	p := b[HeaderSize : HeaderSize+int(mlen)]
	for len(p) > 0 {
		if len(p) < 4 {
			return nil, errors.New("stun: short attr header")
		}
		at := binary.BigEndian.Uint16(p[0:2])
		al := binary.BigEndian.Uint16(p[2:4])
		if int(al)+4 > len(p) {
			return nil, errors.New("stun: attr overflow")
		}
		val := make([]byte, al)
		copy(val, p[4:4+al])
		m.Attributes = append(m.Attributes, Attribute{Type: at, Value: val})
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

func (m *Message) Get(t uint16) ([]byte, bool) {
	for _, a := range m.Attributes {
		if a.Type == t {
			return a.Value, true
		}
	}
	return nil, false
}

// ---- XOR-MAPPED-ADDRESS / XOR-PEER / XOR-RELAYED (mesmo encoding) ----

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

func DecodeXORAddr(v []byte, tid [TIDSize]byte) (*net.UDPAddr, error) {
	if len(v) < 8 {
		return nil, errors.New("stun: short xor-addr")
	}
	xport := binary.BigEndian.Uint16(v[2:4])
	port := int(xport ^ uint16(MagicCookie>>16))
	switch v[1] {
	case familyIPv4:
		cookieBE := make([]byte, 4)
		binary.BigEndian.PutUint32(cookieBE, MagicCookie)
		ip := make(net.IP, 4)
		for i := 0; i < 4; i++ {
			ip[i] = v[4+i] ^ cookieBE[i]
		}
		return &net.UDPAddr{IP: ip, Port: port}, nil
	case familyIPv6:
		if len(v) < 20 {
			return nil, errors.New("stun: short ipv6 xor")
		}
		mask := make([]byte, 16)
		binary.BigEndian.PutUint32(mask[0:4], MagicCookie)
		copy(mask[4:], tid[:])
		ip := make(net.IP, 16)
		for i := 0; i < 16; i++ {
			ip[i] = v[4+i] ^ mask[i]
		}
		return &net.UDPAddr{IP: ip, Port: port}, nil
	}
	return nil, fmt.Errorf("stun: bad family %x", v[1])
}

// ---- MESSAGE-INTEGRITY ----

func AppendMessageIntegrity(raw, key []byte) []byte {
	totalLen := len(raw) - HeaderSize + 24
	tmp := make([]byte, len(raw))
	copy(tmp, raw)
	binary.BigEndian.PutUint16(tmp[2:4], uint16(totalLen))
	mac := hmac.New(sha1.New, key)
	mac.Write(tmp)
	return appendAttr(tmp, AttrMessageIntegrity, mac.Sum(nil))
}

// VerifyMessageIntegrity assume MI como último atributo (sem FINGERPRINT depois).
func VerifyMessageIntegrity(raw, key []byte) bool {
	if len(raw) < HeaderSize+24 {
		return false
	}
	miStart := len(raw) - 24
	if binary.BigEndian.Uint16(raw[miStart:miStart+2]) != AttrMessageIntegrity {
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
	pseudo := make([]byte, miStart)
	copy(pseudo, raw[:miStart])
	totalLen := miStart - HeaderSize + 24
	binary.BigEndian.PutUint16(pseudo[2:4], uint16(totalLen))
	mac := hmac.New(sha1.New, key)
	mac.Write(pseudo)
	return hmac.Equal(mac.Sum(nil), raw[miStart+4:miStart+24])
}

// ---- FINGERPRINT ----

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

// ---- Long-term credential key ----
// key = MD5(username ":" realm ":" password)  — RFC 5389 §15.4
func LongTermKey(user, realm, pass string) []byte {
	h := md5.New()
	h.Write([]byte(user + ":" + realm + ":" + pass))
	return h.Sum(nil)
}

// ---- ERROR-CODE ----
func EncodeErrorCode(code int, reason string) []byte {
	class := byte(code / 100)
	number := byte(code % 100)
	v := make([]byte, 4+len(reason))
	v[2] = class & 0x07
	v[3] = number
	copy(v[4:], reason)
	return v
}

// ---- LIFETIME / REQUESTED-TRANSPORT helpers ----
func EncodeUint32(x uint32) []byte {
	v := make([]byte, 4)
	binary.BigEndian.PutUint32(v, x)
	return v
}
func DecodeUint32(v []byte) uint32 {
	if len(v) < 4 {
		return 0
	}
	return binary.BigEndian.Uint32(v)
}
