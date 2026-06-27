package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestIVFWriterRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.ivf")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	iw, err := NewIVFWriter(f, "VP80", 0, 0, 30, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := iw.UpdateDimensions(640, 480); err != nil {
		t.Fatal(err)
	}
	if err := iw.WriteFrame([]byte{0xAA, 0xBB, 0xCC}, 0); err != nil {
		t.Fatal(err)
	}
	if err := iw.WriteFrame([]byte{0xDD}, 1); err != nil {
		t.Fatal(err)
	}
	if err := iw.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data[0:4], []byte("DKIF")) {
		t.Fatalf("bad magic: %x", data[0:4])
	}
	if !bytes.Equal(data[8:12], []byte("VP80")) {
		t.Fatalf("bad fourcc: %x", data[8:12])
	}
	w := binary.LittleEndian.Uint16(data[12:14])
	h := binary.LittleEndian.Uint16(data[14:16])
	if w != 640 || h != 480 {
		t.Fatalf("dims errado: %dx%d", w, h)
	}
	fc := binary.LittleEndian.Uint32(data[24:28])
	if fc != 2 {
		t.Fatalf("frame_count = %d, esperava 2", fc)
	}
	// primeiro frame começa em offset 32
	sz := binary.LittleEndian.Uint32(data[32:36])
	if sz != 3 {
		t.Fatalf("frame size = %d", sz)
	}
}

func TestOggOpusWriterPages(t *testing.T) {
	var buf bytes.Buffer
	ow, err := NewOggOpusWriter(&buf, 0xDEADBEEF, 2, 48000)
	if err != nil {
		t.Fatal(err)
	}
	if err := ow.WritePacket([]byte{0xFC, 0xFF}, 960); err != nil {
		t.Fatal(err)
	}
	if err := ow.WritePacket([]byte{0xFC}, 960); err != nil {
		t.Fatal(err)
	}
	if err := ow.Close(); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	// Espera ≥ 4 pages: ID, Tags, dois pacotes, EOS.
	count := 0
	for i := 0; i+4 <= len(data); i++ {
		if bytes.Equal(data[i:i+4], []byte("OggS")) {
			count++
		}
	}
	if count < 4 {
		t.Fatalf("pages = %d, esperava >= 4", count)
	}
	// Primeira page deve conter OpusHead.
	if !bytes.Contains(data, []byte("OpusHead")) {
		t.Fatal("OpusHead ausente")
	}
	if !bytes.Contains(data, []byte("OpusTags")) {
		t.Fatal("OpusTags ausente")
	}
}
