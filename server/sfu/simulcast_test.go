package main

import (
	"encoding/binary"
	"strings"
	"testing"
)

func TestParseOneByteExtRID(t *testing.T) {
	// extmap ID 4, valor "hq"
	ext := []byte{(4 << 4) | 1, 'h', 'q'} // ID=4, L=1 → 2 bytes valor
	got := ParseOneByteExt(0xBEDE, ext, 4)
	if string(got) != "hq" {
		t.Fatalf("want hq, got %q", got)
	}
	// ID errado
	if ParseOneByteExt(0xBEDE, ext, 5) != nil {
		t.Fatal("expected nil for unknown id")
	}
	// Profile errado
	if ParseOneByteExt(0x1000, ext, 4) != nil {
		t.Fatal("expected nil for two-byte profile")
	}
}

func TestParseOneByteExtSkipsPadding(t *testing.T) {
	// padding + ID=3 L=0 ("q") + ID=4 L=2 ("xyz")
	ext := []byte{0, 0, (3 << 4) | 0, 'q', (4 << 4) | 2, 'x', 'y', 'z'}
	if string(ParseOneByteExt(0xBEDE, ext, 3)) != "q" {
		t.Fatal("rid q not found")
	}
	if string(ParseOneByteExt(0xBEDE, ext, 4)) != "xyz" {
		t.Fatal("rid xyz not found")
	}
}

func TestLayerRank(t *testing.T) {
	cases := map[string]int{"q": 0, "h": 1, "f": 2, "l": 0, "m": 1, "zzz": -1}
	for in, want := range cases {
		if got := LayerRank(in); got != want {
			t.Errorf("LayerRank(%q)=%d want %d", in, got, want)
		}
	}
}

func TestSDPParseSimulcast(t *testing.T) {
	sdp := strings.Join([]string{
		"v=0",
		"o=- 1 2 IN IP4 0.0.0.0",
		"s=-",
		"t=0 0",
		"m=video 9 UDP/TLS/RTP/SAVPF 96",
		"a=mid:0",
		"a=ice-ufrag:abc",
		"a=ice-pwd:xyzxyzxyzxyzxyzxyzxyz",
		"a=fingerprint:sha-256 AA:BB",
		"a=setup:actpass",
		"a=sendonly",
		"a=extmap:4 urn:ietf:params:rtp-hdrext:sdes:rtp-stream-id",
		"a=extmap:5 urn:ietf:params:rtp-hdrext:sdes:repaired-rtp-stream-id",
		"a=rid:q send",
		"a=rid:h send",
		"a=rid:f send",
		"a=simulcast:send q;h;f",
	}, "\r\n") + "\r\n"
	desc, err := ParseOffer(sdp)
	if err != nil {
		t.Fatal(err)
	}
	if len(desc.Media) != 1 {
		t.Fatalf("want 1 media, got %d", len(desc.Media))
	}
	m := desc.Media[0]
	if m.RIDExtID != 4 {
		t.Errorf("RIDExtID want 4 got %d", m.RIDExtID)
	}
	if m.RRIDExtID != 5 {
		t.Errorf("RRIDExtID want 5 got %d", m.RRIDExtID)
	}
	if len(m.RIDs) != 3 || m.RIDs[0] != "q" || m.RIDs[2] != "f" {
		t.Errorf("RIDs unexpected: %v", m.RIDs)
	}
	if m.Simulcast != "send q;h;f" {
		t.Errorf("simulcast unexpected: %q", m.Simulcast)
	}
	// Answer deve devolver simulcast recv e rid:* recv
	ans := BuildAnswer(desc, AnswerParams{IceUfrag: "u", IcePwd: "p", Fingerprint: "sha-256 CC", HostIP: "1.2.3.4", HostPort: 7000})
	if !strings.Contains(ans, "a=simulcast:recv q;h;f") {
		t.Errorf("answer missing simulcast recv:\n%s", ans)
	}
	for _, rid := range []string{"q", "h", "f"} {
		if !strings.Contains(ans, "a=rid:"+rid+" recv") {
			t.Errorf("answer missing a=rid:%s recv", rid)
		}
	}
}

func TestSessionRememberLayer(t *testing.T) {
	s := &Session{}
	if !s.rememberLayer("h", 111) {
		t.Fatal("first remember should return true")
	}
	if s.rememberLayer("h", 111) {
		t.Fatal("duplicate remember should return false")
	}
	s.rememberLayer("q", 222)
	s.rememberLayer("f", 333)
	if s.layerOfSSRC(222) != "q" {
		t.Errorf("layerOfSSRC(222) = %q", s.layerOfSSRC(222))
	}
	avail := s.availableLayers()
	if len(avail) != 3 || avail[0] != "q" || avail[2] != "f" {
		t.Errorf("availableLayers order wrong: %v", avail)
	}
}

func TestSessionPrefLayer(t *testing.T) {
	s := &Session{}
	s.setPrefLayer("pub1", "q")
	if got := s.getPrefLayer("pub1"); got != "q" {
		t.Errorf("got %q", got)
	}
	if got := s.getPrefLayer("missing"); got != "" {
		t.Errorf("missing should be empty, got %q", got)
	}
}

func TestBuildREMB(t *testing.T) {
	pkt := BuildREMB(0xDEADBEEF, 500_000, []uint32{0x1111, 0x2222})
	// 16 header + 4 brword + 8 ssrcs = 28 bytes; length = 28/4-1 = 6
	if len(pkt) != 28 {
		t.Fatalf("len %d", len(pkt))
	}
	if pkt[0] != 0x80|FBFmtREMB || pkt[1] != RTCPPSFB {
		t.Errorf("bad header %x %x", pkt[0], pkt[1])
	}
	if l := binary.BigEndian.Uint16(pkt[2:4]); l != 6 {
		t.Errorf("length word %d want 6", l)
	}
	if string(pkt[12:16]) != "REMB" {
		t.Errorf("missing REMB id")
	}
	if pkt[16] != 2 {
		t.Errorf("num ssrcs %d", pkt[16])
	}
	if binary.BigEndian.Uint32(pkt[20:24]) != 0x1111 {
		t.Error("ssrc0 wrong")
	}
	// Verifica que decodifica perto de 500k (exp+mantissa lossy)
	word := uint32(pkt[17])<<16 | uint32(pkt[18])<<8 | uint32(pkt[19])
	exp := word >> 18
	mant := word & 0x3FFFF
	got := uint64(mant) << exp
	if got < 400_000 || got > 600_000 {
		t.Errorf("bitrate decoded %d not near 500000", got)
	}
}

func TestVP8KeyframeDetection(t *testing.T) {
	// Descriptor: X=0, S=1, PID=0 (byte 0 = 0x10)
	// VP8 payload header byte 0: P bit (bit 0) = 0 → key
	pkt := []byte{0x10, 0x00, 0x00, 0x00}
	if !VP8IsKeyframe(pkt) {
		t.Error("should detect keyframe")
	}
	// P=1 → delta frame
	pkt[1] = 0x01
	if VP8IsKeyframe(pkt) {
		t.Error("should detect delta")
	}
	// S=0 → não é início de partição 0
	pkt = []byte{0x00, 0x00}
	if VP8IsKeyframe(pkt) {
		t.Error("S=0 should not be keyframe")
	}
}

func TestH264KeyframeDetection(t *testing.T) {
	// Single NAL IDR (type 5)
	if !H264IsKeyframe([]byte{0x65, 0x88, 0x80}) {
		t.Error("IDR not detected")
	}
	// SPS (type 7)
	if !H264IsKeyframe([]byte{0x67, 0x42}) {
		t.Error("SPS not detected")
	}
	// Non-IDR slice (type 1) → delta
	if H264IsKeyframe([]byte{0x61, 0x88}) {
		t.Error("non-IDR should be delta")
	}
	// FU-A start of IDR: byte0=0x7C (FU-A), byte1=0x85 (S=1, type=5)
	if !H264IsKeyframe([]byte{0x7C, 0x85, 0x00}) {
		t.Error("FU-A IDR start not detected")
	}
}
