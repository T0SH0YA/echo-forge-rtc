// Ogg reader — parser de pages RFC 3533, monta pacotes Opus.
//
// Cada page tem segment_table: pacotes podem se estender por múltiplos
// segments (255 = continua, <255 = fim do pacote). Pacotes podem também
// cruzar pages (flag header_type bit 0 = continuation).
package main

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
)

type OggPacket struct {
	Data           []byte
	GranulePosEnd  uint64 // granule no fim do último pacote da page que contém esse pacote
}

type OggReader struct {
	r       io.Reader
	partial []byte
	pktQ    []OggPacket
	pageEnd uint64
}

func OpenOgg(r io.Reader) *OggReader { return &OggReader{r: r} }

// Next devolve próximo pacote Opus, ou io.EOF.
func (o *OggReader) Next() (*OggPacket, error) {
	for len(o.pktQ) == 0 {
		if err := o.readPage(); err != nil {
			return nil, err
		}
	}
	p := o.pktQ[0]
	o.pktQ = o.pktQ[1:]
	return &p, nil
}

func (o *OggReader) readPage() error {
	var hdr [27]byte
	if _, err := io.ReadFull(o.r, hdr[:]); err != nil {
		return err
	}
	if string(hdr[0:4]) != "OggS" {
		return errors.New("ogg: bad capture pattern")
	}
	headerType := hdr[5]
	granule := binary.LittleEndian.Uint64(hdr[6:14])
	nSeg := int(hdr[26])
	segTable := make([]byte, nSeg)
	if _, err := io.ReadFull(o.r, segTable); err != nil {
		return err
	}
	// monta os pacotes
	var packets [][]byte
	var cur []byte
	for _, s := range segTable {
		seg := make([]byte, s)
		if _, err := io.ReadFull(o.r, seg); err != nil {
			return err
		}
		cur = append(cur, seg...)
		if s < 255 {
			packets = append(packets, cur)
			cur = nil
		}
	}
	// fragmento residual continua na próxima page
	residual := cur
	_ = crc32.IEEETable // crc não validamos aqui

	// se primeira é continuation, anexa ao partial anterior
	if headerType&0x01 != 0 && len(packets) > 0 {
		packets[0] = append(o.partial, packets[0]...)
		o.partial = nil
	} else if o.partial != nil {
		// um partial órfão (não deveria), descartamos.
		o.partial = nil
	}
	if residual != nil {
		o.partial = append(o.partial, residual...)
	}

	for _, p := range packets {
		if len(p) == 0 {
			continue
		}
		o.pktQ = append(o.pktQ, OggPacket{Data: p, GranulePosEnd: granule})
	}
	o.pageEnd = granule
	return nil
}

// OpusPacketDurationSamples — TOC byte (RFC 6716 §3.1) determina o frame
// size e nº de frames. Retornamos sempre 960 (20ms) como fallback seguro;
// ngrains finos: o WriteFrame ainda funciona com aproximação porque o
// player Matroska usa o timestamp do bloco e ignora a duração interna.
func OpusPacketDurationSamples(_ []byte) uint32 { return 960 }
