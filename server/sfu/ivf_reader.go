// IVF reader — parser do container que nosso IVFWriter produz.
package main

import (
	"encoding/binary"
	"errors"
	"io"
)

type IVFFile struct {
	FourCC     string
	Width      uint16
	Height     uint16
	FpsNum     uint32
	FpsDen     uint32
	FrameCount uint32

	r io.Reader
}

type IVFFrame struct {
	Data []byte
	PTS  uint64
}

func OpenIVF(r io.Reader) (*IVFFile, error) {
	var hdr [32]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	if string(hdr[0:4]) != "DKIF" {
		return nil, errors.New("not IVF")
	}
	f := &IVFFile{
		FourCC:     string(hdr[8:12]),
		Width:      binary.LittleEndian.Uint16(hdr[12:14]),
		Height:     binary.LittleEndian.Uint16(hdr[14:16]),
		FpsNum:     binary.LittleEndian.Uint32(hdr[16:20]),
		FpsDen:     binary.LittleEndian.Uint32(hdr[20:24]),
		FrameCount: binary.LittleEndian.Uint32(hdr[24:28]),
		r:          r,
	}
	if f.FpsNum == 0 {
		f.FpsNum = 30
	}
	if f.FpsDen == 0 {
		f.FpsDen = 1
	}
	return f, nil
}

// Next devolve io.EOF quando termina.
func (f *IVFFile) Next() (*IVFFrame, error) {
	var fh [12]byte
	if _, err := io.ReadFull(f.r, fh[:]); err != nil {
		return nil, err
	}
	size := binary.LittleEndian.Uint32(fh[0:4])
	pts := binary.LittleEndian.Uint64(fh[4:12])
	data := make([]byte, size)
	if _, err := io.ReadFull(f.r, data); err != nil {
		return nil, err
	}
	return &IVFFrame{Data: data, PTS: pts}, nil
}

// TimestampMs converte PTS IVF (em ticks de FpsDen/FpsNum) pra milissegundos.
func (f *IVFFile) TimestampMs(pts uint64) int64 {
	// dur_per_tick_ms = 1000 * FpsDen / FpsNum
	return int64(pts) * 1000 * int64(f.FpsDen) / int64(f.FpsNum)
}

// IsVP8Keyframe: bit0 do byte0 == 0 (RFC 6386 §9.1).
func IsVP8Keyframe(frame []byte) bool {
	return len(frame) > 0 && frame[0]&0x01 == 0
}
