package main

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func TestSRTCPRoundtrip(t *testing.T) {
	key := make([]byte, 16)
	salt := make([]byte, 12)
	_, _ = rand.Read(key)
	_, _ = rand.Read(salt)
	tx, _ := NewSRTCPContext(key, salt)
	rx, _ := NewSRTCPContext(key, salt)

	// RTCP Sender Report mínimo: 28 bytes (header 8 + sender info 20).
	pkt := make([]byte, 28)
	pkt[0] = 0x80
	pkt[1] = RTCPSR
	binary.BigEndian.PutUint16(pkt[2:4], 6) // (28/4)-1
	binary.BigEndian.PutUint32(pkt[4:8], 0xDEADBEEF)
	copy(pkt[8:], bytes.Repeat([]byte{0xAB}, 20))

	cipher, err := tx.Encrypt(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if len(cipher) != len(pkt)+16+4 {
		t.Fatalf("bad cipher len %d", len(cipher))
	}
	// Header em claro preservado
	if !bytes.Equal(cipher[:8], pkt[:8]) {
		t.Fatal("header tampered")
	}
	// Body cifrado != plaintext
	if bytes.Equal(cipher[8:8+20], pkt[8:]) {
		t.Fatal("body not encrypted")
	}
	plain, err := rx.Decrypt(cipher)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plain, pkt) {
		t.Fatal("roundtrip mismatch")
	}
}

func TestSRTCPTamperRejected(t *testing.T) {
	key := make([]byte, 16)
	salt := make([]byte, 12)
	tx, _ := NewSRTCPContext(key, salt)
	pkt := append([]byte{0x80, RTCPSR, 0, 6, 0, 0, 0, 1}, bytes.Repeat([]byte{1}, 20)...)
	cipher, _ := tx.Encrypt(pkt)
	cipher[10] ^= 0xFF
	rx, _ := NewSRTCPContext(key, salt)
	if _, err := rx.Decrypt(cipher); err == nil {
		t.Fatal("tampered srtcp must fail")
	}
}

func TestRTCPSplitCompound(t *testing.T) {
	// Compound: RR (8 bytes header-only) + PSFB-PLI (12 bytes)
	rr := []byte{0x80, RTCPRR, 0, 1, 0, 0, 0, 0xAA}
	pli := BuildPLI(0x11111111, 0xCAFEBABE)
	buf := append(append([]byte{}, rr...), pli...)
	pkts, err := SplitCompound(buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkts) != 2 {
		t.Fatalf("want 2 pkts, got %d", len(pkts))
	}
	if pkts[0].PayloadType != RTCPRR {
		t.Fatalf("first not RR")
	}
	if !pkts[1].IsPLI() {
		t.Fatal("second not PLI")
	}
	if pkts[1].MediaSSRC != 0xCAFEBABE {
		t.Fatalf("media ssrc mismatch %x", pkts[1].MediaSSRC)
	}
}

// TestRouterFeedbackUpstream: publisher P publica SSRC X.
// Subscriber S manda PLI(mediaSSRC=X). Esperamos receber o PLI no socket
// do publisher, decifrável com sua srtcpSend.
func TestRouterFeedbackUpstream(t *testing.T) {
	srvUDP, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	defer srvUDP.Close()
	router := NewRouter(srvUDP)

	mk := func(name byte) (*Session, *net.UDPConn) {
		sock, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
		ckey := bytes.Repeat([]byte{0xC0 | name}, 16)
		csalt := bytes.Repeat([]byte{0xC1 | name}, 12)
		skey := bytes.Repeat([]byte{0x50 | name}, 16)
		ssalt := bytes.Repeat([]byte{0x51 | name}, 12)
		sRecv, _ := NewSRTPContext(ckey, csalt)
		sSend, _ := NewSRTPContext(skey, ssalt)
		rRecv, _ := NewSRTCPContext(ckey, csalt)
		rSend, _ := NewSRTCPContext(skey, ssalt)
		ss := &Session{
			ID:         string(rune(name)),
			srtpRecv:   sRecv, srtpSend: sSend,
			srtcpRecv: rRecv, srtcpSend: rSend,
			remoteAddr: sock.LocalAddr().String(),
		}
		return ss, sock
	}
	pub, pubSock := mk('p')
	defer pubSock.Close()
	sub, _ := mk('s')

	router.Add(pub)
	router.Add(sub)

	// Publisher publica RTP com SSRC X — registra ownership.
	pubEnc, _ := NewSRTPContext(bytes.Repeat([]byte{0xC0 | 'p'}, 16), bytes.Repeat([]byte{0xC1 | 'p'}, 12))
	const SSRC = uint32(0xDEAFBEEF)
	rtp := buildRTP(SSRC, 1, 0, []byte("hello"))
	cipher, _ := pubEnc.Encrypt(rtp, 12, SSRC, 1)
	router.HandleRTP(pub, cipher)

	// Subscriber manda PLI cifrado pra X.
	pli := BuildPLI(0x99999999, SSRC)
	// Subscriber cifra com ClientKey/Salt (lado "envio" do cliente).
	subClientEnc, _ := NewSRTCPContext(bytes.Repeat([]byte{0xC0 | 's'}, 16), bytes.Repeat([]byte{0xC1 | 's'}, 12))
	subCipher, _ := subClientEnc.Encrypt(pli)
	router.HandleRTCP(sub, subCipher)

	// Publisher recebe PLI cifrado com ServerKey/Salt do publisher (decifra com mesma chave).
	pubServerDec, _ := NewSRTCPContext(bytes.Repeat([]byte{0x50 | 'p'}, 16), bytes.Repeat([]byte{0x51 | 'p'}, 12))
	_ = pubSock.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1500)
	n, _, err := pubSock.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("publisher read: %v", err)
	}
	plain, err := pubServerDec.Decrypt(buf[:n])
	if err != nil {
		t.Fatalf("publisher srtcp decrypt: %v", err)
	}
	pkts, err := SplitCompound(plain)
	if err != nil || len(pkts) != 1 || !pkts[0].IsPLI() {
		t.Fatalf("expected single PLI, got %v err=%v", pkts, err)
	}
	if pkts[0].MediaSSRC != SSRC {
		t.Fatalf("media ssrc wrong: %x", pkts[0].MediaSSRC)
	}
}
