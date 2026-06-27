// Ogg/Opus writer — RFC 3533 (Ogg) + RFC 7845 (Opus in Ogg).
//
// Cada Ogg page tem:
//   "OggS" | version(0) | header_type | granule_pos(8 LE) | bitstream_serial(4 LE)
//   | page_seq(4 LE) | crc32(4 LE, calc com campo CRC=0) | n_segments(1)
//   | segment_table(n_segments) | segments...
//
// Pra Opus, gravamos sempre 1 pacote por page (simples e válido). Os dois
// primeiros pages contêm os headers obrigatórios: ID Header e Comment Header.
// Granule = nº de samples PCM @ 48kHz acumulados.
package main

import (
	"encoding/binary"
	"hash/crc32"
	"io"
)

var oggCRCTable = crc32.MakeTable(0x04C11DB7)

type OggWriter struct {
	w        io.Writer
	serial   uint32
	pageSeq  uint32
	granule  uint64
	channels uint8
	rate     uint32
}

func NewOggOpusWriter(w io.Writer, serial uint32, channels uint8, sampleRate uint32) (*OggWriter, error) {
	ow := &OggWriter{w: w, serial: serial, channels: channels, rate: sampleRate}
	if err := ow.writeIDHeader(); err != nil {
		return nil, err
	}
	if err := ow.writeCommentHeader(); err != nil {
		return nil, err
	}
	return ow, nil
}

// "OpusHead" v1 (RFC 7845 §5.1) — 19 bytes mínimos.
func (ow *OggWriter) writeIDHeader() error {
	h := make([]byte, 19)
	copy(h[0:8], "OpusHead")
	h[8] = 1                                        // version
	h[9] = ow.channels                              // channel count
	binary.LittleEndian.PutUint16(h[10:12], 0)      // pre-skip
	binary.LittleEndian.PutUint32(h[12:16], ow.rate) // input sample rate
	binary.LittleEndian.PutUint16(h[16:18], 0)      // output gain
	h[18] = 0                                       // channel mapping family
	return ow.writePage(h, 0, 0x02 /* BOS */, 0)
}

// "OpusTags" mínimo: 8 + 4 + 0 (vendor len 0) + 4 (user comment count 0) = 16.
func (ow *OggWriter) writeCommentHeader() error {
	h := make([]byte, 16)
	copy(h[0:8], "OpusTags")
	// vendor_length = 0, user_comment_list_length = 0 → restante zerado.
	return ow.writePage(h, 0, 0, 0)
}

// WritePacket grava 1 pacote Opus. samples = nº de samples PCM @ 48kHz que
// esse pacote representa (típico 960 = 20ms; o caller pode passar 0 e a
// gente assume 960).
func (ow *OggWriter) WritePacket(opus []byte, samples uint32) error {
	if samples == 0 {
		samples = 960
	}
	ow.granule += uint64(samples)
	return ow.writePage(opus, ow.granule, 0, 0)
}

// Close emite uma page final com flag EOS (0x04).
func (ow *OggWriter) Close() error {
	if err := ow.writePage(nil, ow.granule, 0x04, 0); err != nil {
		return err
	}
	if c, ok := ow.w.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

func (ow *OggWriter) writePage(packet []byte, granule uint64, headerType byte, _ int) error {
	// Segment table: tamanho do pacote em lassos de 255. Pacote vazio → 0 segs.
	var segs []byte
	if len(packet) == 0 {
		// EOS sem dados: 1 segmento de tamanho 0.
		segs = []byte{0}
	} else {
		n := len(packet)
		for n >= 255 {
			segs = append(segs, 255)
			n -= 255
		}
		segs = append(segs, byte(n))
		if len(segs) > 255 {
			// (não acontece em pacotes Opus reais, mas guardamos invariant)
			return io.ErrShortBuffer
		}
	}
	headerLen := 27 + len(segs)
	page := make([]byte, headerLen+len(packet))
	copy(page[0:4], "OggS")
	page[4] = 0
	page[5] = headerType
	binary.LittleEndian.PutUint64(page[6:14], granule)
	binary.LittleEndian.PutUint32(page[14:18], ow.serial)
	binary.LittleEndian.PutUint32(page[18:22], ow.pageSeq)
	// CRC zerado pra cálculo:
	binary.LittleEndian.PutUint32(page[22:26], 0)
	page[26] = byte(len(segs))
	copy(page[27:27+len(segs)], segs)
	copy(page[headerLen:], packet)
	crc := crc32.Checksum(page, oggCRCTable)
	binary.LittleEndian.PutUint32(page[22:26], crc)
	if _, err := ow.w.Write(page); err != nil {
		return err
	}
	ow.pageSeq++
	return nil
}
