package main

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

// vetor SRTP-GCM construído por roundtrip: encrypt → decrypt → match plaintext.
// Não há vetor oficial reutilizável aqui porque o RFC 7714 §17 traz só vetores
// de keying material; encrypt/decrypt roundtrip cobre o fluxo completo
// (build IV, AAD = header, GCM seal/open).

func TestSRTPRoundtrip(t *testing.T) {
	key := make([]byte, 16)
	salt := make([]byte, 12)
	_, _ = rand.Read(key)
	_, _ = rand.Read(salt)
	ctxA, err := NewSRTPContext(key, salt)
	if err != nil {
		t.Fatal(err)
	}
	ctxB, err := NewSRTPContext(key, salt)
	if err != nil {
		t.Fatal(err)
	}

	hdr := []byte{
		0x80, 0x60, 0x00, 0x2A, // V=2, PT=96, SEQ=42
		0x00, 0x00, 0x10, 0x00, // TS
		0xDE, 0xAD, 0xBE, 0xEF, // SSRC
	}
	payload := []byte("hello srtp gcm world")
	pkt := append(append([]byte{}, hdr...), payload...)

	parsed, err := ParseRTP(pkt)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := ctxA.Encrypt(pkt, parsed.HeaderLen, parsed.SSRC, parsed.SequenceNumber)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(cipher[parsed.HeaderLen:parsed.HeaderLen+len(payload)], payload) {
		t.Fatal("ciphertext equals plaintext — GCM didn't run")
	}
	if len(cipher) != parsed.HeaderLen+len(payload)+16 {
		t.Fatalf("bad cipher len %d, want %d", len(cipher), parsed.HeaderLen+len(payload)+16)
	}
	// header preservado
	if !bytes.Equal(cipher[:parsed.HeaderLen], pkt[:parsed.HeaderLen]) {
		t.Fatal("header tampered")
	}

	plain, err := ctxB.Decrypt(cipher, parsed.HeaderLen, parsed.SSRC, parsed.SequenceNumber)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(plain, pkt) {
		t.Fatalf("roundtrip mismatch")
	}
}

func TestSRTPTamperRejected(t *testing.T) {
	key := make([]byte, 16)
	salt := make([]byte, 12)
	ctx, _ := NewSRTPContext(key, salt)
	pkt := append([]byte{0x80, 0x60, 0, 1, 0, 0, 0, 1, 0, 0, 0, 1}, []byte("data")...)
	cipher, _ := ctx.Encrypt(pkt, 12, 1, 1)
	cipher[len(cipher)-1] ^= 0x01 // flip tag bit
	ctx2, _ := NewSRTPContext(key, salt)
	if _, err := ctx2.Decrypt(cipher, 12, 1, 1); err == nil {
		t.Fatal("tampered packet should fail auth")
	}
}

func TestSRTPROCWrap(t *testing.T) {
	key := make([]byte, 16)
	salt := make([]byte, 12)
	ctx, _ := NewSRTPContext(key, salt)
	// SEQ alto não dispara wrap
	if r := ctx.rocFor(7, 0xFFFE); r != 0 {
		t.Fatalf("first seq, want roc=0 got %d", r)
	}
	// wrap pra 0x0001
	if r := ctx.rocFor(7, 0x0001); r != 1 {
		t.Fatalf("after wrap, want roc=1 got %d", r)
	}
	// SEQ adiante normal mantém ROC=1
	if r := ctx.rocFor(7, 0x0100); r != 1 {
		t.Fatalf("post-wrap forward, want roc=1 got %d", r)
	}
}

// TestRouterForward1to2: três sessões fake, cada uma com socket UDP próprio
// pra receber forwarded packets. publisher envia 5 RTPs cifrados;
// subscribers recebem todos decifráveis com suas chaves.
func TestRouterForward1to2(t *testing.T) {
	srvUDP, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer srvUDP.Close()
	router := NewRouter(srvUDP)

	mk := func(name string) (*Session, *net.UDPConn) {
		sock, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
		if err != nil {
			t.Fatal(err)
		}
		// Chaves fake: cliente cifra com "C…", servidor cifra com "S…".
		ckey := bytes.Repeat([]byte{0xC0 | name[0]}, 16)
		csalt := bytes.Repeat([]byte{0xC1 | name[0]}, 12)
		skey := bytes.Repeat([]byte{0x50 | name[0]}, 16)
		ssalt := bytes.Repeat([]byte{0x51 | name[0]}, 12)
		recv, _ := NewSRTPContext(ckey, csalt)
		send, _ := NewSRTPContext(skey, ssalt)
		s := &Session{
			ID:         name,
			srtpRecv:   recv,
			srtpSend:   send,
			remoteAddr: sock.LocalAddr().String(),
		}
		return s, sock
	}

	pub, pubSock := mk("p")
	defer pubSock.Close()
	subA, sockA := mk("a")
	defer sockA.Close()
	subB, sockB := mk("b")
	defer sockB.Close()

	router.Add(pub)
	router.Add(subA)
	router.Add(subB)

	// Cliente "decifrador" de cada subscriber: usa o MESMO send key da sessão
	// como contexto de recebimento (server→client, mesma chave).
	decA, _ := NewSRTPContext(bytes.Repeat([]byte{0x50 | 'a'}, 16), bytes.Repeat([]byte{0x51 | 'a'}, 12))
	decB, _ := NewSRTPContext(bytes.Repeat([]byte{0x50 | 'b'}, 16), bytes.Repeat([]byte{0x51 | 'b'}, 12))

	// Publisher contexto "cliente→server": cifra com ckey/csalt do pub.
	pubEnc, _ := NewSRTPContext(bytes.Repeat([]byte{0xC0 | 'p'}, 16), bytes.Repeat([]byte{0xC1 | 'p'}, 12))

	for i := 0; i < 5; i++ {
		seq := uint16(100 + i)
		pkt := buildRTP(0xCAFEBABE, seq, uint32(i*960), []byte("payload-data-"+string(rune('A'+i))))
		cipher, err := pubEnc.Encrypt(pkt, 12, 0xCAFEBABE, seq)
		if err != nil {
			t.Fatal(err)
		}
		router.HandleRTP(pub, cipher)
	}

	readN := func(sock *net.UDPConn, dec *SRTPContext, label string) {
		_ = sock.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 1500)
		for i := 0; i < 5; i++ {
			n, _, err := sock.ReadFromUDP(buf)
			if err != nil {
				t.Fatalf("%s read %d: %v", label, i, err)
			}
			hdr, err := ParseRTP(buf[:n])
			if err != nil {
				t.Fatalf("%s parse: %v", label, err)
			}
			plain, err := dec.Decrypt(buf[:n], hdr.HeaderLen, hdr.SSRC, hdr.SequenceNumber)
			if err != nil {
				t.Fatalf("%s decrypt seq=%d: %v", label, hdr.SequenceNumber, err)
			}
			if hdr.SSRC != 0xCAFEBABE {
				t.Fatalf("%s ssrc mismatch %x", label, hdr.SSRC)
			}
			wantSeq := uint16(100 + i)
			if hdr.SequenceNumber != wantSeq {
				t.Fatalf("%s seq want %d got %d", label, wantSeq, hdr.SequenceNumber)
			}
			if !bytes.HasPrefix(plain[hdr.HeaderLen:], []byte("payload-data-")) {
				t.Fatalf("%s payload bad: %q", label, plain[hdr.HeaderLen:])
			}
		}
	}
	readN(sockA, decA, "A")
	readN(sockB, decB, "B")

	if rtpFwd.Load() < 10 {
		t.Fatalf("expected ≥10 forwarded, got %d", rtpFwd.Load())
	}
}

func buildRTP(ssrc uint32, seq uint16, ts uint32, payload []byte) []byte {
	b := make([]byte, 12+len(payload))
	b[0] = 0x80
	b[1] = 96
	binary.BigEndian.PutUint16(b[2:4], seq)
	binary.BigEndian.PutUint32(b[4:8], ts)
	binary.BigEndian.PutUint32(b[8:12], ssrc)
	copy(b[12:], payload)
	return b
}

func TestIsRTPDemux(t *testing.T) {
	if !IsRTPOrRTCP([]byte{0x80, 96}) {
		t.Fatal("RTP not detected")
	}
	if !IsRTCP([]byte{0x80, 200}) {
		t.Fatal("RTCP SR not detected")
	}
	if IsRTCP([]byte{0x80, 96}) {
		t.Fatal("RTP misdetected as RTCP")
	}
	if IsRTPOrRTCP([]byte{0x00, 0x00}) {
		t.Fatal("STUN misdetected as RTP")
	}
}
