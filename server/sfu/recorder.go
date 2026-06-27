// Recorder — gravação server-side, tecnologia própria (sem ffmpeg).
//
// VP8 → IVF: reassembla frames pela marker bit RTP + S bit do payload
//   descriptor VP8 (RFC 7741 §4.2). Primeiro keyframe atualiza width/height
//   no header IVF (RFC 6386 §9.1 — uncompressed header).
//
// Opus → Ogg: 1 pacote Opus por page; granule incrementa em 960 samples
//   (20ms @ 48kHz) por padrão — tolera variação no caller.
//
// Etapa 17: controle HTTP por sessão + manifesto JSON.
//   - SFU_RECORD_DIR habilita o subsistema; se SFU_RECORD_AUTOSTART=1 toda
//     sessão começa gravando, caso contrário precisa POST /sessions/{id}/record/start.
//   - Arquivos por trilha: <dir>/<sessionID>/<ssrc>[__rid].<ivf|ogg>
//   - Manifesto: <dir>/<sessionID>/manifest.json (regravado a cada open/close).
package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

var (
	recBytes  atomic.Uint64
	recFrames atomic.Uint64
	recOpen   atomic.Uint64
)

// TrackManifest descreve uma trilha gravada (1 SSRC).
type TrackManifest struct {
	SSRC      uint32 `json:"ssrc"`
	RID       string `json:"rid,omitempty"`
	Codec     string `json:"codec"`
	ClockRate uint32 `json:"clockRate"`
	File      string `json:"file"`
	OpenedAt  string `json:"openedAt"`
	ClosedAt  string `json:"closedAt,omitempty"`
	Frames    uint64 `json:"frames"`
	Bytes     uint64 `json:"bytes"`
	Width     uint16 `json:"width,omitempty"`
	Height    uint16 `json:"height,omitempty"`
	// StartOffsetMs: tempo entre o início da sessão (sr.startedAt) e o
	// primeiro pacote desta trilha. Usado pelo mux pra A/V sync — o player
	// vai posicionar o primeiro bloco da trilha exatamente nesse offset.
	StartOffsetMs int64 `json:"startOffsetMs"`
	// FirstRtpTs: timestamp RTP do primeiro pacote gravado, em ticks do
	// próprio clock da trilha. Pareado com StartOffsetMs permite reconstruir
	// timeline exata caso o mux precise relativizar a trilhas terceiras.
	FirstRtpTs uint32 `json:"firstRtpTs,omitempty"`
}

// SessionManifest é o documento gravado em manifest.json.
type SessionManifest struct {
	SessionID string           `json:"sessionId"`
	StartedAt string           `json:"startedAt"`
	StoppedAt string           `json:"stoppedAt,omitempty"`
	Active    bool             `json:"active"`
	Tracks    []*TrackManifest `json:"tracks"`
}

type sessionRec struct {
	id        string
	dir       string
	startedAt time.Time
	stoppedAt time.Time
	active    bool
	streams   map[uint32]*streamRecorder // ssrc → recorder
}

type RecorderHub struct {
	dir       string
	autostart bool

	mu       sync.Mutex
	sessions map[string]*sessionRec
}

// NewRecorderHub: sempre retorna um hub se SFU_RECORD_DIR estiver setado.
// Sem env → nil (gravação desligada por completo).
func NewRecorderHub() *RecorderHub {
	dir := os.Getenv("SFU_RECORD_DIR")
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("[rec] mkdir %s: %v", dir, err)
		return nil
	}
	auto := os.Getenv("SFU_RECORD_AUTOSTART") == "1"
	log.Printf("[rec] enabled dir=%s autostart=%v", dir, auto)
	return &RecorderHub{dir: dir, autostart: auto, sessions: map[string]*sessionRec{}}
}

func (h *RecorderHub) Enabled() bool { return h != nil }

// Start ativa gravação para uma sessão. Idempotente.
func (h *RecorderHub) Start(sessionID string) error {
	if h == nil {
		return errors.New("recorder disabled (set SFU_RECORD_DIR)")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	sr, ok := h.sessions[sessionID]
	if !ok {
		sr = &sessionRec{id: sessionID, streams: map[uint32]*streamRecorder{}}
		sr.dir = filepath.Join(h.dir, sessionID)
		if err := os.MkdirAll(sr.dir, 0o755); err != nil {
			return err
		}
		h.sessions[sessionID] = sr
	}
	if !sr.active {
		sr.active = true
		sr.startedAt = time.Now().UTC()
		sr.stoppedAt = time.Time{}
	}
	h.writeManifestLocked(sr)
	return nil
}

// Stop encerra gravação da sessão, fechando todos os streams.
func (h *RecorderHub) Stop(sessionID string) error {
	if h == nil {
		return errors.New("recorder disabled")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	sr, ok := h.sessions[sessionID]
	if !ok {
		return errors.New("session not recording")
	}
	if sr.active {
		sr.active = false
		sr.stoppedAt = time.Now().UTC()
		for _, r := range sr.streams {
			r.close()
		}
	}
	h.writeManifestLocked(sr)
	return nil
}

// Manifest retorna o manifesto serializável (snapshot consistente).
func (h *RecorderHub) Manifest(sessionID string) (*SessionManifest, error) {
	if h == nil {
		return nil, errors.New("recorder disabled")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	sr, ok := h.sessions[sessionID]
	if !ok {
		return nil, errors.New("not found")
	}
	return h.snapshotLocked(sr), nil
}

func (h *RecorderHub) snapshotLocked(sr *sessionRec) *SessionManifest {
	m := &SessionManifest{
		SessionID: sr.id,
		StartedAt: ts(sr.startedAt),
		Active:    sr.active,
	}
	if !sr.stoppedAt.IsZero() {
		m.StoppedAt = ts(sr.stoppedAt)
	}
	for _, r := range sr.streams {
		m.Tracks = append(m.Tracks, r.snapshot())
	}
	sort.Slice(m.Tracks, func(i, j int) bool { return m.Tracks[i].SSRC < m.Tracks[j].SSRC })
	return m
}

func (h *RecorderHub) writeManifestLocked(sr *sessionRec) {
	m := h.snapshotLocked(sr)
	path := filepath.Join(sr.dir, "manifest.json")
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		log.Printf("[rec] manifest %s: %v", path, err)
		return
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		_ = f.Close()
		log.Printf("[rec] manifest encode: %v", err)
		return
	}
	_ = f.Close()
	_ = os.Rename(tmp, path)
}

func ts(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339Nano)
}

// On chamado pela router (post-jitter, em ordem) para cada pacote do publisher.
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

	h.mu.Lock()
	sr, ok := h.sessions[pub.ID]
	if !ok {
		if !h.autostart {
			h.mu.Unlock()
			return
		}
		sr = &sessionRec{id: pub.ID, streams: map[uint32]*streamRecorder{}, dir: filepath.Join(h.dir, pub.ID)}
		_ = os.MkdirAll(sr.dir, 0o755)
		sr.active = true
		sr.startedAt = time.Now().UTC()
		h.sessions[pub.ID] = sr
	}
	if !sr.active {
		h.mu.Unlock()
		return
	}
	r, ok := sr.streams[hdr.SSRC]
	dirty := false
	if !ok {
		r = h.createStream(pub, sr, hdr.SSRC, codec, clock)
		if r != nil {
			sr.streams[hdr.SSRC] = r
			dirty = true
		}
	}
	h.mu.Unlock()
	if dirty {
		h.mu.Lock()
		h.writeManifestLocked(sr)
		h.mu.Unlock()
	}
	if r == nil {
		return
	}
	r.write(hdr, payload)
}

// CloseSSRC fecha o gravador associado quando publisher sai.
func (h *RecorderHub) CloseSSRC(pubID string, ssrc uint32) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	sr, ok := h.sessions[pubID]
	if !ok {
		return
	}
	r := sr.streams[ssrc]
	if r != nil {
		r.close()
		h.writeManifestLocked(sr)
	}
}

// CloseSession fecha e remove o estado in-memory da sessão.
func (h *RecorderHub) CloseSession(pubID string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	sr, ok := h.sessions[pubID]
	if !ok {
		return
	}
	for _, r := range sr.streams {
		r.close()
	}
	if sr.active {
		sr.active = false
		sr.stoppedAt = time.Now().UTC()
	}
	h.writeManifestLocked(sr)
}

func (h *RecorderHub) createStream(pub *Session, sr *sessionRec, ssrc uint32, codec string, clock uint32) *streamRecorder {
	rid := pub.layerOfSSRC(ssrc)
	suffix := ""
	if rid != "" {
		suffix = "__" + rid
	}
	switch codec {
	case "vp8":
		name := fmt.Sprintf("%d%s.ivf", ssrc, suffix)
		path := filepath.Join(sr.dir, name)
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
		return &streamRecorder{codec: "vp8", ivf: iw, clock: clock, ssrc: ssrc, rid: rid, file: name, openedAt: time.Now().UTC(), sessionStart: sr.startedAt}
	case "opus":
		name := fmt.Sprintf("%d%s.ogg", ssrc, suffix)
		path := filepath.Join(sr.dir, name)
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
		return &streamRecorder{codec: "opus", ogg: ow, clock: clock, ssrc: ssrc, rid: rid, file: name, openedAt: time.Now().UTC(), sessionStart: sr.startedAt}
	}
	return nil
}

type streamRecorder struct {
	codec    string
	mu       sync.Mutex
	clock    uint32
	ssrc     uint32
	rid      string
	file     string
	openedAt time.Time
	closedAt time.Time
	closed   bool

	// Offset/sync (Etapa 19): primeira escrita determina o offset relativo
	// ao início da sessão; persistido no manifesto pro mux pós-call alinhar
	// vídeo e áudio na timeline real.
	sessionStart  time.Time
	offsetSet     atomic.Bool
	startOffsetMs atomic.Int64
	firstRtpTs    atomic.Uint32

	frames atomic.Uint64
	bytes  atomic.Uint64
	width  atomic.Uint32
	height atomic.Uint32

	// VP8
	ivf      *IVFWriter
	frameBuf []byte
	firstTS  uint32
	tsSet    bool
	dimsSet  bool

	// Opus
	ogg *OggWriter
}

func (r *streamRecorder) snapshot() *TrackManifest {
	t := &TrackManifest{
		SSRC: r.ssrc, RID: r.rid, Codec: r.codec, ClockRate: r.clock,
		File: r.file, OpenedAt: ts(r.openedAt),
		Frames: r.frames.Load(), Bytes: r.bytes.Load(),
		Width: uint16(r.width.Load()), Height: uint16(r.height.Load()),
		StartOffsetMs: r.startOffsetMs.Load(),
		FirstRtpTs:    r.firstRtpTs.Load(),
	}
	if !r.closedAt.IsZero() {
		t.ClosedAt = ts(r.closedAt)
	}
	return t
}

// markFirst registra offset relativo à sessão na primeira escrita.
func (r *streamRecorder) markFirst(hdr *RTPHeader) {
	if r.offsetSet.CompareAndSwap(false, true) {
		var ms int64
		if !r.sessionStart.IsZero() {
			ms = time.Since(r.sessionStart).Milliseconds()
			if ms < 0 {
				ms = 0
			}
		}
		r.startOffsetMs.Store(ms)
		r.firstRtpTs.Store(hdr.Timestamp)
	}
}

func (r *streamRecorder) write(hdr *RTPHeader, payload []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.markFirst(hdr)
	switch r.codec {
	case "vp8":
		r.writeVP8(hdr, payload)
	case "opus":
		_ = r.ogg.WritePacket(payload, 960)
		r.bytes.Add(uint64(len(payload)))
		r.frames.Add(1)
		recBytes.Add(uint64(len(payload)))
		recFrames.Add(1)
	}
}

// writeVP8: payload começa com o VP8 Payload Descriptor (RFC 7741 §4.2).
func (r *streamRecorder) writeVP8(hdr *RTPHeader, payload []byte) {
	if len(payload) < 1 {
		return
	}
	off := 1
	b0 := payload[0]
	if b0&0x80 != 0 { // X
		if len(payload) < 2 {
			return
		}
		x := payload[1]
		off = 2
		if x&0x80 != 0 {
			if len(payload) < off+1 {
				return
			}
			if payload[off]&0x80 != 0 {
				off += 2
			} else {
				off++
			}
		}
		if x&0x40 != 0 {
			off++
		}
		if x&0x20 != 0 || x&0x10 != 0 {
			off++
		}
	}
	if off >= len(payload) {
		return
	}
	vp8 := payload[off:]
	startOfFrame := (b0&0x10 != 0) && (b0&0x07 == 0)
	if startOfFrame || !r.tsSet || hdr.Timestamp != r.firstTS {
		if len(r.frameBuf) > 0 && r.tsSet && hdr.Timestamp == r.firstTS {
			// continuação
		} else {
			r.frameBuf = r.frameBuf[:0]
			r.firstTS = hdr.Timestamp
			r.tsSet = true
		}
	}
	r.frameBuf = append(r.frameBuf, vp8...)

	if hdr.Marker {
		if !r.dimsSet && len(r.frameBuf) >= 10 && r.frameBuf[0]&0x01 == 0 {
			if r.frameBuf[3] == 0x9d && r.frameBuf[4] == 0x01 && r.frameBuf[5] == 0x2a {
				w := binary.LittleEndian.Uint16(r.frameBuf[6:8]) & 0x3fff
				h := binary.LittleEndian.Uint16(r.frameBuf[8:10]) & 0x3fff
				_ = r.ivf.UpdateDimensions(w, h)
				r.width.Store(uint32(w))
				r.height.Store(uint32(h))
				r.dimsSet = true
			}
		}
		pts := uint64(hdr.Timestamp) / uint64(r.clock/30)
		if err := r.ivf.WriteFrame(r.frameBuf, pts); err == nil {
			r.frames.Add(1)
			r.bytes.Add(uint64(len(r.frameBuf)))
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
	if r.closed {
		return
	}
	r.closed = true
	r.closedAt = time.Now().UTC()
	if r.ivf != nil {
		_ = r.ivf.Close()
	}
	if r.ogg != nil {
		_ = r.ogg.Close()
	}
}
