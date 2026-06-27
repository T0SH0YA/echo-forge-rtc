// WebM writer — EBML/Matroska próprio, escopo: VP8 vídeo + Opus áudio.
//
// Especificações usadas:
//   - EBML: https://www.rfc-editor.org/rfc/rfc8794.html
//   - Matroska/WebM: https://www.matroska.org/technical/elements.html
//                    https://www.webmproject.org/docs/container/
//
// Layout produzido:
//
//	EBML(header)
//	Segment (unknown size)
//	  Info            (TimecodeScale=1ms, MuxingApp, WritingApp, Duration)
//	  Tracks
//	    TrackEntry V_VP8  (TrackNumber=1, PixelW/H)
//	    TrackEntry A_OPUS (TrackNumber=2, CodecPrivate=OpusHead, 48kHz, ch)
//	  Cluster*        (Timecode + SimpleBlock*)
//
// SimpleBlock: VINT(track) | int16BE(relTC ms) | flags(1) | frameData
//   flags bit 7 = keyframe (necessário pro player decodificar VP8).
package main

import (
	"encoding/binary"
	"io"
	"math"
)

// IDs Matroska (forma canônica completa, com marker bit).
const (
	idEBML            = 0x1A45DFA3
	idEBMLVersion     = 0x4286
	idEBMLReadVer     = 0x42F7
	idEBMLMaxIDLen    = 0x42F2
	idEBMLMaxSizeLen  = 0x42F3
	idDocType         = 0x4282
	idDocTypeVer      = 0x4287
	idDocTypeReadVer  = 0x4285

	idSegment = 0x18538067

	idInfo          = 0x1549A966
	idTimecodeScale = 0x2AD7B1
	idMuxingApp     = 0x4D80
	idWritingApp    = 0x5741
	idDuration      = 0x4489

	idTracks      = 0x1654AE6B
	idTrackEntry  = 0xAE
	idTrackNumber = 0xD7
	idTrackUID    = 0x73C5
	idTrackType   = 0x83
	idFlagLacing  = 0x9C
	idCodecID     = 0x86
	idCodecPriv   = 0x63A2

	idVideo       = 0xE0
	idPixelWidth  = 0xB0
	idPixelHeight = 0xBA

	idAudio    = 0xE1
	idSampFreq = 0xB5
	idChannels = 0x9F

	idCluster      = 0x1F43B675
	idTimecode     = 0xE7
	idSimpleBlock  = 0xA3
)

// ===== EBML primitives =====

// writeID escreve o ID já em sua forma "canônica" (incluindo marker bit).
// IDs em Matroska são opacos: a gente só serializa os bytes que carregam.
func putID(w io.Writer, id uint32) error {
	var b [4]byte
	n := 0
	switch {
	case id >= 0x10000000:
		binary.BigEndian.PutUint32(b[:], id)
		n = 4
	case id >= 0x200000:
		b[0] = byte(id >> 16)
		b[1] = byte(id >> 8)
		b[2] = byte(id)
		n = 3
	case id >= 0x4000:
		b[0] = byte(id >> 8)
		b[1] = byte(id)
		n = 2
	default:
		b[0] = byte(id)
		n = 1
	}
	_, err := w.Write(b[:n])
	return err
}

// vintLen escolhe o menor nº de bytes (1..8) capaz de codificar v com
// marker bit. Não usa o "all-ones" reservado pra unknown size.
func vintBytes(v uint64) []byte {
	for n := 1; n <= 8; n++ {
		max := uint64(1)<<(7*uint(n)) - 1
		if v < max {
			out := make([]byte, n)
			binary.BigEndian.PutUint64(append(make([]byte, 8-n), out...), 0)
			// monta com marker
			tmp := v | (uint64(1) << uint(7*n))
			for i := n - 1; i >= 0; i-- {
				out[i] = byte(tmp)
				tmp >>= 8
			}
			return out
		}
	}
	panic("vint overflow")
}

func putVINT(w io.Writer, v uint64) error {
	_, err := w.Write(vintBytes(v))
	return err
}

// vintUnknown size: 0x01 0xFF*7 (8 bytes), reservado pra "tamanho desconhecido".
var vintUnknown = []byte{0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}

// putElem escreve ID + size(VINT) + payload.
func putElem(w io.Writer, id uint32, payload []byte) error {
	if err := putID(w, id); err != nil {
		return err
	}
	if err := putVINT(w, uint64(len(payload))); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// putUint codifica uint sem zeros à esquerda (1..8 bytes), como Matroska espera.
func putUint(id uint32, v uint64) []byte {
	var tmp [8]byte
	binary.BigEndian.PutUint64(tmp[:], v)
	i := 0
	for i < 7 && tmp[i] == 0 {
		i++
	}
	body := tmp[i:]
	out := make([]byte, 0, 4+len(body))
	out = appendID(out, id)
	out = append(out, vintBytes(uint64(len(body)))...)
	out = append(out, body...)
	return out
}

func putString(id uint32, s string) []byte {
	out := appendID(nil, id)
	out = append(out, vintBytes(uint64(len(s)))...)
	out = append(out, s...)
	return out
}

func putBinary(id uint32, b []byte) []byte {
	out := appendID(nil, id)
	out = append(out, vintBytes(uint64(len(b)))...)
	out = append(out, b...)
	return out
}

func putFloat32(id uint32, f float32) []byte {
	var tmp [4]byte
	binary.BigEndian.PutUint32(tmp[:], math.Float32bits(f))
	out := appendID(nil, id)
	out = append(out, vintBytes(4)...)
	out = append(out, tmp[:]...)
	return out
}

func appendID(dst []byte, id uint32) []byte {
	switch {
	case id >= 0x10000000:
		return append(dst, byte(id>>24), byte(id>>16), byte(id>>8), byte(id))
	case id >= 0x200000:
		return append(dst, byte(id>>16), byte(id>>8), byte(id))
	case id >= 0x4000:
		return append(dst, byte(id>>8), byte(id))
	default:
		return append(dst, byte(id))
	}
}

func wrap(id uint32, children ...[]byte) []byte {
	body := 0
	for _, c := range children {
		body += len(c)
	}
	out := appendID(nil, id)
	out = append(out, vintBytes(uint64(body))...)
	for _, c := range children {
		out = append(out, c...)
	}
	return out
}

// ===== WebM writer =====

const (
	trackVideo = 1
	trackAudio = 2

	clusterWindowMs = 4000 // novo cluster a cada 4s
)

type WebMWriter struct {
	w io.Writer

	hasVideo bool
	hasAudio bool

	curClusterStartMs int64
	curClusterOpen    bool
	pendingBlocks     []byte
	maxTcMs           int64
}

// NewWebMWriter abre EBML+Segment e escreve Tracks. videoW/H podem ser 0
// (alguns players inferem do bitstream VP8).
func NewWebMWriter(w io.Writer, hasVideo bool, vw, vh uint16, hasAudio bool, opusHead []byte, channels uint8) (*WebMWriter, error) {
	ww := &WebMWriter{w: w, hasVideo: hasVideo, hasAudio: hasAudio}

	// EBML header
	ebml := wrap(idEBML,
		putUint(idEBMLVersion, 1),
		putUint(idEBMLReadVer, 1),
		putUint(idEBMLMaxIDLen, 4),
		putUint(idEBMLMaxSizeLen, 8),
		putString(idDocType, "webm"),
		putUint(idDocTypeVer, 4),
		putUint(idDocTypeReadVer, 2),
	)
	if _, err := w.Write(ebml); err != nil {
		return nil, err
	}

	// Segment com unknown size (streaming-friendly).
	if err := putID(w, idSegment); err != nil {
		return nil, err
	}
	if _, err := w.Write(vintUnknown); err != nil {
		return nil, err
	}

	// Info
	info := wrap(idInfo,
		putUint(idTimecodeScale, 1_000_000), // 1ms
		putString(idMuxingApp, softwareName),
		putString(idWritingApp, softwareName),
	)
	if _, err := w.Write(info); err != nil {
		return nil, err
	}

	// Tracks
	var entries [][]byte
	if hasVideo {
		video := wrap(idVideo,
			putUint(idPixelWidth, uint64(maxU16(vw, 320))),
			putUint(idPixelHeight, uint64(maxU16(vh, 240))),
		)
		te := wrap(idTrackEntry,
			putUint(idTrackNumber, trackVideo),
			putUint(idTrackUID, 0xA10A10),
			putUint(idTrackType, 1),
			putUint(idFlagLacing, 0),
			putString(idCodecID, "V_VP8"),
			video,
		)
		entries = append(entries, te)
	}
	if hasAudio {
		audio := wrap(idAudio,
			putFloat32(idSampFreq, 48000),
			putUint(idChannels, uint64(channels)),
		)
		te := wrap(idTrackEntry,
			putUint(idTrackNumber, trackAudio),
			putUint(idTrackUID, 0xB20B20),
			putUint(idTrackType, 2),
			putUint(idFlagLacing, 0),
			putString(idCodecID, "A_OPUS"),
			putBinary(idCodecPriv, opusHead),
			audio,
		)
		entries = append(entries, te)
	}
	tracks := wrap(idTracks, entries...)
	if _, err := w.Write(tracks); err != nil {
		return nil, err
	}
	return ww, nil
}

func maxU16(a, b uint16) uint16 {
	if a > b {
		return a
	}
	return b
}

// WriteFrame adiciona um frame a um cluster. keyframe importa para VP8.
// trackNum=1 (video) ou 2 (audio). ts em ms desde início do mux.
func (ww *WebMWriter) WriteFrame(trackNum uint8, tsMs int64, keyframe bool, data []byte) error {
	if !ww.curClusterOpen {
		ww.startCluster(tsMs)
	}
	rel := tsMs - ww.curClusterStartMs
	if rel < -32768 || rel > 32767 {
		// flush e abre novo cluster
		if err := ww.flushCluster(); err != nil {
			return err
		}
		ww.startCluster(tsMs)
		rel = 0
	} else if rel-int64(ww.curClusterStartMs-ww.curClusterStartMs) >= clusterWindowMs && keyframe && trackNum == trackVideo {
		// fecha cluster em keyframe se janela cheia
		if err := ww.flushCluster(); err != nil {
			return err
		}
		ww.startCluster(tsMs)
		rel = 0
	}
	if tsMs > ww.maxTcMs {
		ww.maxTcMs = tsMs
	}

	// SimpleBlock: VINT(track) | int16BE rel | flags | data
	var hdr []byte
	hdr = append(hdr, vintBytes(uint64(trackNum))...)
	hdr = append(hdr, byte(rel>>8), byte(rel))
	flags := byte(0)
	if keyframe {
		flags |= 0x80
	}
	hdr = append(hdr, flags)
	body := make([]byte, 0, len(hdr)+len(data))
	body = append(body, hdr...)
	body = append(body, data...)
	block := appendID(nil, idSimpleBlock)
	block = append(block, vintBytes(uint64(len(body)))...)
	block = append(block, body...)
	ww.pendingBlocks = append(ww.pendingBlocks, block...)
	return nil
}

func (ww *WebMWriter) startCluster(tsMs int64) {
	ww.curClusterStartMs = tsMs
	ww.curClusterOpen = true
	ww.pendingBlocks = ww.pendingBlocks[:0]
}

func (ww *WebMWriter) flushCluster() error {
	if !ww.curClusterOpen {
		return nil
	}
	tc := putUint(idTimecode, uint64(ww.curClusterStartMs))
	body := make([]byte, 0, len(tc)+len(ww.pendingBlocks))
	body = append(body, tc...)
	body = append(body, ww.pendingBlocks...)
	if err := putElem(ww.w, idCluster, body); err != nil {
		return err
	}
	ww.curClusterOpen = false
	ww.pendingBlocks = ww.pendingBlocks[:0]
	return nil
}

func (ww *WebMWriter) Close() error {
	return ww.flushCluster()
}
