// IVF writer — container simples pra VP8 (e VP9), tecnologia própria.
//
// Layout (little-endian):
//
//	File header (32 bytes):
//	  [0..3]  "DKIF"
//	  [4..5]  version  = 0
//	  [6..7]  header_len = 32
//	  [8..11] fourcc   (VP80 / VP90)
//	  [12..13] width
//	  [14..15] height
//	  [16..19] framerate numerator
//	  [20..23] framerate denominator
//	  [24..27] frame_count (preenchemos no Close)
//	  [28..31] reserved
//
//	Frame header (12 bytes):
//	  [0..3]  frame_size (bytes do frame que segue)
//	  [4..11] timestamp (presentation, em "framerate" ticks; usamos PTS direto)
//
// Width/height são preenchidos com 0 e atualizados ao parsear o primeiro
// keyframe VP8 (3 bytes "show_frame" + size = 10 bytes header VP8 → bytes
// 6..9 contém width|hScale, 8..9 height|vScale, na uncompressed key frame
// header RFC 6386 §9.1). Tolerante: se não der, fica 0 e players modernos
// (ffmpeg/mpv) lidam.
package main

import (
	"encoding/binary"
	"io"
)

type IVFWriter struct {
	w          io.WriteSeeker
	frameCount uint32
	fourcc     [4]byte
	header     [32]byte
}

func NewIVFWriter(w io.WriteSeeker, fourcc string, width, height uint16, fpsNum, fpsDen uint32) (*IVFWriter, error) {
	iw := &IVFWriter{w: w}
	copy(iw.fourcc[:], fourcc)
	hdr := iw.header[:]
	copy(hdr[0:4], "DKIF")
	binary.LittleEndian.PutUint16(hdr[4:6], 0)
	binary.LittleEndian.PutUint16(hdr[6:8], 32)
	copy(hdr[8:12], iw.fourcc[:])
	binary.LittleEndian.PutUint16(hdr[12:14], width)
	binary.LittleEndian.PutUint16(hdr[14:16], height)
	binary.LittleEndian.PutUint32(hdr[16:20], fpsNum)
	binary.LittleEndian.PutUint32(hdr[20:24], fpsDen)
	// frame_count fica zerado; atualizamos no Close.
	if _, err := w.Write(hdr); err != nil {
		return nil, err
	}
	return iw, nil
}

// WriteFrame grava um frame completo já reassemblado com seu PTS.
func (iw *IVFWriter) WriteFrame(frame []byte, pts uint64) error {
	var fh [12]byte
	binary.LittleEndian.PutUint32(fh[0:4], uint32(len(frame)))
	binary.LittleEndian.PutUint64(fh[4:12], pts)
	if _, err := iw.w.Write(fh[:]); err != nil {
		return err
	}
	if _, err := iw.w.Write(frame); err != nil {
		return err
	}
	iw.frameCount++
	return nil
}

// UpdateDimensions reescreve width/height no header (chamado quando a key
// frame nos der tamanho real). Idempotente.
func (iw *IVFWriter) UpdateDimensions(width, height uint16) error {
	binary.LittleEndian.PutUint16(iw.header[12:14], width)
	binary.LittleEndian.PutUint16(iw.header[14:16], height)
	pos, err := iw.w.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if _, err := iw.w.Seek(12, io.SeekStart); err != nil {
		return err
	}
	if _, err := iw.w.Write(iw.header[12:16]); err != nil {
		return err
	}
	_, err = iw.w.Seek(pos, io.SeekStart)
	return err
}

// Close fixa o frame_count e fecha o writer.
func (iw *IVFWriter) Close() error {
	pos, err := iw.w.Seek(0, io.SeekCurrent)
	if err == nil {
		var fc [4]byte
		binary.LittleEndian.PutUint32(fc[:], iw.frameCount)
		if _, err := iw.w.Seek(24, io.SeekStart); err == nil {
			_, _ = iw.w.Write(fc[:])
			_, _ = iw.w.Seek(pos, io.SeekStart)
		}
	}
	if c, ok := iw.w.(io.Closer); ok {
		return c.Close()
	}
	return nil
}
