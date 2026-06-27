package main

import (
	"bytes"
	"net"
	"testing"
	"time"
)

func TestNACKBuildParseRoundtrip(t *testing.T) {
	// Perdas: 100, 102, 103, 117 (= 100+17, fica fora do BLP do 100 → novo FCI)
	lost := []uint16{100, 102, 103, 117}
	pkt := BuildNACK(0xAAAA, 0xBBBB, lost)
	pkts, err := SplitCompound(pkt)
	if err != nil || len(pkts) != 1 {
		t.Fatalf("split: %v len=%d", err, len(pkts))
	}
	if !pkts[0].IsNACK() {
		t.Fatal("not NACK")
	}
	if pkts[0].MediaSSRC != 0xBBBB {
		t.Fatalf("media ssrc %x", pkts[0].MediaSSRC)
	}
	got := ParseNACK(pkts[0])
	want := map[uint16]bool{100: true, 102: true, 103: true, 117: true}
	if len(got) != len(want) {
		t.Fatalf("want %d seqs got %v", len(want), got)
	}
	for _, s := range got {
		if !want[s] {
			t.Fatalf("unexpected seq %d in %v", s, got)
		}
	}
}

func TestRTXCachePutGet(t *testing.T) {
	c := NewRTXCache()
	c.Put(7, 42, 12, []byte("aaaaaaaaaaaapayload"))
	hl, plain, ok := c.Get(7, 42)
	if !ok || hl != 12 || string(plain[12:]) != "payload" {
		t.Fatalf("get failed ok=%v hl=%d plain=%q", ok, hl, plain)
	}
	if _, _, ok := c.Get(7, 99); ok {
		t.Fatal("phantom hit")
	}
	if _, _, ok := c.Get(9, 42); ok {
		t.Fatal("wrong ssrc hit")
	}
}

// TestRouterRTXAnswersNACK: publisher publica 5 pacotes (cached).
// Subscriber manda NACK pedindo seqs 101, 103. SFU reenvia esses dois
// re-cifrados com srtpSend do subscriber — sem nada chegar ao publisher.
func TestRouterRTXAnswersNACK(t *testing.T) {
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
		return &Session{
			ID:         string(rune(name)),
			srtpRecv:   sRecv, srtpSend: sSend,
			srtcpRecv: rRecv, srtcpSend: rSend,
			remoteAddr: sock.LocalAddr().String(),
		}, sock
	}
	pub, pubSock := mk('p')
	defer pubSock.Close()
	sub, subSock := mk('s')
	defer subSock.Close()

	router.Add(pub)
	router.Add(sub)

	const SSRC = uint32(0xC0FFEE01)
	pubEnc, _ := NewSRTPContext(bytes.Repeat([]byte{0xC0 | 'p'}, 16), bytes.Repeat([]byte{0xC1 | 'p'}, 12))
	for i := 0; i < 5; i++ {
		seq := uint16(100 + i)
		rtp := buildRTP(SSRC, seq, uint32(i), []byte{byte('A' + i), '_', 'p', 'a', 'y'})
		cipher, _ := pubEnc.Encrypt(rtp, 12, SSRC, seq)
		router.HandleRTP(pub, cipher)
	}
	// Drena os 5 forwards normais que foram pro subscriber.
	_ = subSock.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1500)
	for i := 0; i < 5; i++ {
		if _, _, err := subSock.ReadFromUDP(buf); err != nil {
			t.Fatalf("drain fwd %d: %v", i, err)
		}
	}

	// Subscriber pede NACK pra seqs 101 e 103.
	nack := BuildNACK(0xDEADBEEF, SSRC, []uint16{101, 103})
	subClientSRTCP, _ := NewSRTCPContext(bytes.Repeat([]byte{0xC0 | 's'}, 16), bytes.Repeat([]byte{0xC1 | 's'}, 12))
	cipher, _ := subClientSRTCP.Encrypt(nack)
	beforeHit := rtxHit.Load()
	router.HandleRTCP(sub, cipher)

	// Publisher NÃO deve receber nada (NACK foi consumido).
	_ = pubSock.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	if n, _, err := pubSock.ReadFromUDP(buf); err == nil {
		t.Fatalf("publisher recebeu %d bytes — NACK não deveria subir", n)
	}

	// Subscriber recebe DOIS pacotes RTX (re-cifrados com sua server key).
	subServerSRTP, _ := NewSRTPContext(bytes.Repeat([]byte{0x50 | 's'}, 16), bytes.Repeat([]byte{0x51 | 's'}, 12))
	_ = subSock.SetReadDeadline(time.Now().Add(2 * time.Second))
	gotSeqs := map[uint16]bool{}
	for i := 0; i < 2; i++ {
		n, _, err := subSock.ReadFromUDP(buf)
		if err != nil {
			t.Fatalf("rtx read %d: %v", i, err)
		}
		hdr, err := ParseRTP(buf[:n])
		if err != nil {
			t.Fatalf("parse rtx: %v", err)
		}
		plain, err := subServerSRTP.Decrypt(buf[:n], hdr.HeaderLen, hdr.SSRC, hdr.SequenceNumber)
		if err != nil {
			t.Fatalf("decrypt rtx seq=%d: %v", hdr.SequenceNumber, err)
		}
		if hdr.SSRC != SSRC {
			t.Fatalf("rtx ssrc wrong %x", hdr.SSRC)
		}
		if string(plain[hdr.HeaderLen+1:hdr.HeaderLen+5]) != "_pay" {
			t.Fatalf("rtx payload wrong %q", plain[hdr.HeaderLen:])
		}
		gotSeqs[hdr.SequenceNumber] = true
	}
	if !gotSeqs[101] || !gotSeqs[103] {
		t.Fatalf("want seqs {101,103}, got %v", gotSeqs)
	}
	if rtxHit.Load()-beforeHit != 2 {
		t.Fatalf("want 2 rtx hits, got %d", rtxHit.Load()-beforeHit)
	}
}
