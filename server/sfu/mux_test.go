package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// Constrói um IVF + Ogg fakes, roda MuxSession, valida que o WebM
// começa com EBML header + Segment + Tracks + ao menos um Cluster.
func TestMuxSession_WebM(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SFU_RECORD_DIR", dir)
	t.Setenv("SFU_RECORD_AUTOSTART", "1")
	h := NewRecorderHub()
	if h == nil {
		t.Fatal("hub nil")
	}
	sessID := "s1"
	if err := h.Start(sessID); err != nil {
		t.Fatal(err)
	}
	sessDir := filepath.Join(dir, sessID)

	// IVF: 3 frames, primeiro é keyframe sintético com start code 9d 01 2a.
	vidPath := filepath.Join(sessDir, "11111.ivf")
	vf, err := os.Create(vidPath)
	if err != nil {
		t.Fatal(err)
	}
	iw, err := NewIVFWriter(vf, "VP80", 320, 240, 30, 1)
	if err != nil {
		t.Fatal(err)
	}
	// frame keyframe: byte0=0x00 (key), bytes3..5 = 9d 01 2a, w=320 h=240
	kf := make([]byte, 30)
	kf[3], kf[4], kf[5] = 0x9d, 0x01, 0x2a
	binary.LittleEndian.PutUint16(kf[6:8], 320)
	binary.LittleEndian.PutUint16(kf[8:10], 240)
	if err := iw.WriteFrame(kf, 0); err != nil {
		t.Fatal(err)
	}
	// inter-frames: byte0 bit0=1
	interF := []byte{0x01, 0xAA, 0xBB}
	if err := iw.WriteFrame(interF, 1); err != nil {
		t.Fatal(err)
	}
	if err := iw.WriteFrame(interF, 2); err != nil {
		t.Fatal(err)
	}
	if err := iw.Close(); err != nil {
		t.Fatal(err)
	}

	// Ogg: 4 pacotes Opus "fake" (qualquer payload).
	audPath := filepath.Join(sessDir, "22222.ogg")
	af, err := os.Create(audPath)
	if err != nil {
		t.Fatal(err)
	}
	ow, err := NewOggOpusWriter(af, 22222, 2, 48000)
	if err != nil {
		t.Fatal(err)
	}
	pkt := []byte{0xfc, 0x00, 0x11, 0x22, 0x33}
	for i := 0; i < 4; i++ {
		if err := ow.WritePacket(pkt, 960); err != nil {
			t.Fatal(err)
		}
	}
	if err := ow.Close(); err != nil {
		t.Fatal(err)
	}

	// Injeta as trilhas no manifesto manualmente (sem ir pelo pipeline).
	h.mu.Lock()
	sr := h.sessions[sessID]
	sr.streams[11111] = &streamRecorder{codec: "vp8", clock: 90000, ssrc: 11111, file: "11111.ivf"}
	sr.streams[11111].frames.Store(3)
	sr.streams[11111].width.Store(320)
	sr.streams[11111].height.Store(240)
	sr.streams[22222] = &streamRecorder{codec: "opus", clock: 48000, ssrc: 22222, file: "22222.ogg"}
	sr.streams[22222].frames.Store(4)
	h.mu.Unlock()

	if err := h.Stop(sessID); err != nil {
		t.Fatal(err)
	}

	outPath, err := h.MuxSession(sessID)
	if err != nil {
		t.Fatalf("mux: %v", err)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 64 {
		t.Fatalf("webm too small: %d", len(data))
	}
	// EBML magic
	if !bytes.Equal(data[0:4], []byte{0x1A, 0x45, 0xDF, 0xA3}) {
		t.Fatalf("missing EBML header: %x", data[0:8])
	}
	// Segment ID present
	if !bytes.Contains(data[:64], []byte{0x18, 0x53, 0x80, 0x67}) {
		t.Fatal("missing Segment id")
	}
	// Tracks ID present
	if !bytes.Contains(data, []byte{0x16, 0x54, 0xAE, 0x6B}) {
		t.Fatal("missing Tracks id")
	}
	// Cluster ID present
	if !bytes.Contains(data, []byte{0x1F, 0x43, 0xB6, 0x75}) {
		t.Fatal("missing Cluster id")
	}
	// CodecID "V_VP8" e "A_OPUS"
	if !bytes.Contains(data, []byte("V_VP8")) {
		t.Fatal("missing V_VP8")
	}
	if !bytes.Contains(data, []byte("A_OPUS")) {
		t.Fatal("missing A_OPUS")
	}
}

func TestVintBytes(t *testing.T) {
	cases := []struct {
		v   uint64
		exp []byte
	}{
		{0, []byte{0x80}},
		{1, []byte{0x81}},
		{126, []byte{0xFE}},
		{127, []byte{0x40, 0x7F}}, // 127 é reservado em 1B → vai pra 2B
		{200, []byte{0x40, 0xC8}},
		{16383, []byte{0x20, 0x3F, 0xFF}},
	}
	for _, c := range cases {
		got := vintBytes(c.v)
		if !bytes.Equal(got, c.exp) {
			t.Errorf("vintBytes(%d) = %x, want %x", c.v, got, c.exp)
		}
	}
}
