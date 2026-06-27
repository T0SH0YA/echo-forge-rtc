// Recorder — gravação server-side, tecnologia própria (sem ffmpeg).
//
// VP8 → IVF: reassembla frames pela marker bit RTP + S bit do payload
//   descriptor VP8 (RFC 7741 §4.2). Primeiro keyframe atualiza width/height
//   no header IVF (RFC 6386 §9.1 — uncompressed header).
//
// Opus → Ogg: 1 pacote Opus por page; granule incrementa em 960 samples
//   (20ms @ 48kHz) por padrão — tolera variação no caller.
//
// Ativado por variável de ambiente SFU_RECORD_DIR. Arquivos:
//   <dir>/<sessionID>__<ssrc>__<rid?>.<ivf|ogg>
package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

var (
	recBytes  atomic.Uint64
	recFrames atomic.Uint64
	recOpen   atomic.Uint64
)

type RecorderHub struct {
	dir string

	mu  sync.Mutex
	per map[string]*streamRecorder // key = sessID|ssrc
}

func NewRecorderHub() *RecorderHub {
	dir := os.Getenv("SFU_RECORD_DIR")
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("[rec] mkdir %s: %v", dir, err)
		return nil
	}
	log.Printf("[rec] enabled dir=%s", dir)
	return &RecorderHub{dir: dir, per: map[string]*streamRecorder{}}
}

// On chamado pela router (post-jitter, em ordem de seq) pra cada pacote
// emitido por um publisher.
func (h *RecorderHub) On(pub *Session, hdr *RTPHeader, payload []byte) {
	if h == nil {
		return
	}
	codec := ""
	clock := uint32(48000)
	pub.mu.Lock()
	if pub.PTCodec != nil {
		codec = pub.PTCodec[hdr.PayloadType]
	}
	if pub.PTClock != nil {
		if c := pub.PTClock[hdr.PayloadType]; c != 0 {
			clock = c
		}
	}
	pub.mu.Unlock()
	if codec == "" {
		return
	}
	k := jbKey(pub.ID, hdr.SSRC)
	h.mu.Lock()
	r, ok := h.per[k]
	if !ok {
		r = h.create(pub, hdr.SSRC, codec, clock)
		h.per[k] = r
	}
	h.mu.Unlock()
	if r == nil {
		return
	}
	r.write(hdr, payload)
}

// CloseSSRC fecha o gravador associado (chamado quando publisher sai).
func (h *RecorderHub) CloseSSRC(pubID string, ssrc uint32) {
	if h == nil {
		return
	}
	k := jbKey(pubID, ssrc)
	h.mu.Lock()
	r := h.per[k]
	delete(h.per, k)
	h.mu.Unlock()
	if r != nil {
		r.close()
	}
}

func (h *RecorderHub) create(pub *Session, ssrc uint32, codec string, clock uint32) *streamRecorder {
	rid := pub.layerOfSSRC(ssrc)
	suffix := ""
	if rid != "" {
		suffix = "__" + rid
	}
	switch codec {
	case "vp8":
		path := filepath.Join(h.dir, fmt.Sprintf("%s__%d%s.ivf", pub.ID, ssrc, suffix))
		f, err := os.Create(path)
		if err != nil {
			log.Printf("[rec] create %s: %v", path, err)
			return nil
		}
		iw, err := NewIVFWriter(f, "VP80", 0, 0, 30, 1)
		if err != nil {
			_ = f.Close()
			return nil
		}
		recOpen.Add(1)
		log.Printf("[rec] open vp8 ssrc=%d → %s", ssrc, path)
		return &streamRecorder{codec: "vp8", ivf: iw, clock: clock}
	case "opus":
		path := filepath.Join(h.dir, fmt.Sprintf("%s__%d%s.ogg", pub.ID, ssrc, suffix))
		f, err := os.Create(path)
		if err != nil {
			log.Printf("[rec] create %s: %v", path, err)
			return nil
		}
		ow, err := NewOggOpusWriter(f, ssrc, 2, clock)
		if err != nil {
			_ = f.Close()
			return nil
		}
		recOpen.Add(1)
		log.Printf("[rec] open opus ssrc=%d → %s", ssrc, path)
		return &streamRecorder{codec: "opus", ogg: ow, clock: clock}
	}
	return nil
}

type streamRecorder struct {
	codec string
	mu    sync.Mutex
	clock uint32

	// VP8
	ivf       *IVFWriter
	frameBuf  []byte
	firstTS   uint32
	tsSet     bool
	dimsSet   bool

	// Opus
	ogg *OggWriter
}

func (r *streamRecorder) write(hdr *RTPHeader, payload []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch r.codec {
	case "vp8":
		r.writeVP8(hdr, payload)
	case "opus":
		_ = r.ogg.WritePacket(payload, 960)
		recBytes.Add(uint64(len(payload)))
		recFrames.Add(1)
	}
}

// writeVP8: payload começa com o VP8 Payload Descriptor (RFC 7741 §4.2).
//   byte 0: X|R|N|S|R|PID(3)
// X=1 → extensão. S=1 → start de partição. Para reassembler frame inteiro
// só precisamos: S bit = início de um frame OU continuação. Marker bit RTP
// = último pacote do frame. Concatenamos payload (pulando o descriptor) e
// emitimos quando marker.
func (r *streamRecorder) writeVP8(hdr *RTPHeader, payload []byte) {
	if len(payload) < 1 {
		return
	}
	off := 1
	b0 := payload[0]
	if b0&0x80 != 0 { // X bit
		if len(payload) < 2 {
			return
		}
		x := payload[1]
		off = 2
		if x&0x80 != 0 { // I (PictureID)
			if len(payload) < off+1 {
				return
			}
			if payload[off]&0x80 != 0 { // 15-bit PID
				off += 2
			} else {
				off++
			}
		}
		if x&0x40 != 0 { // L (TL0PICIDX)
			off++
		}
		if x&0x20 != 0 || x&0x10 != 0 { // T or K
			off++
		}
	}
	if off >= len(payload) {
		return
	}
	vp8 := payload[off:]

	// S=1 + PID=0 → começo de novo frame. Marca por timestamp também
	// (timestamp do RTP muda por frame).
	startOfFrame := (b0&0x10 != 0) && (b0&0x07 == 0)
	if startOfFrame || !r.tsSet || hdr.Timestamp != r.firstTS {
		// Frame novo: descarta buffer anterior (incompleto) e começa.
		if len(r.frameBuf) > 0 && r.tsSet && hdr.Timestamp == r.firstTS {
			// continuação do mesmo timestamp — não resetar.
		} else {
			r.frameBuf = r.frameBuf[:0]
			r.firstTS = hdr.Timestamp
			r.tsSet = true
		}
	}
	r.frameBuf = append(r.frameBuf, vp8...)

	if hdr.Marker {
		// Tenta extrair dimensões na primeira keyframe.
		// VP8 keyframe: bit0 do byte 0 da uncompressed header = 0 (frame_type=KEY)
		if !r.dimsSet && len(r.frameBuf) >= 10 && r.frameBuf[0]&0x01 == 0 {
			// bytes 3..5 devem ser start code 0x9d 0x01 0x2a (RFC 6386 §9.1)
			if r.frameBuf[3] == 0x9d && r.frameBuf[4] == 0x01 && r.frameBuf[5] == 0x2a {
				w := binary.LittleEndian.Uint16(r.frameBuf[6:8]) & 0x3fff
				h := binary.LittleEndian.Uint16(r.frameBuf[8:10]) & 0x3fff
				_ = r.ivf.UpdateDimensions(w, h)
				r.dimsSet = true
			}
		}
		// PTS em "ticks" do framerate IVF: dividimos timestamp (90kHz)
		// por (90000/30) = 3000 → frame index aproximado.
		pts := uint64(hdr.Timestamp) / uint64(r.clock/30)
		if err := r.ivf.WriteFrame(r.frameBuf, pts); err == nil {
			recFrames.Add(1)
			recBytes.Add(uint64(len(r.frameBuf)))
		}
		r.frameBuf = r.frameBuf[:0]
		r.tsSet = false
	}
}

func (r *streamRecorder) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ivf != nil {
		_ = r.ivf.Close()
	}
	if r.ogg != nil {
		_ = r.ogg.Close()
	}
}
