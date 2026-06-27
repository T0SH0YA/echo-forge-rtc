// Mux — combina trilhas IVF (VP8) + Ogg (Opus) gravadas pelo SFU num
// WebM único, em Go puro. Cronograma: cada trilha começa em 0; o tempo
// real entre gravações não está disponível nos arquivos (futuramente
// pode-se sidecar com session-start). Alinhamento por trilha:
//   - vídeo: PTS IVF → ms via FpsNum/FpsDen
//   - áudio: granule Ogg @48kHz → ms
//
// Etapa 18: endpoint POST /sessions/{id}/record/mux gera
// <SFU_RECORD_DIR>/<sessionId>/mixed.webm a partir do manifesto.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// MuxSession lê o manifest da sessão e produz mixed.webm com 1 trilha
// de vídeo (primeira VP8) + 1 trilha de áudio (primeira Opus).
func (h *RecorderHub) MuxSession(sessionID string) (string, error) {
	if h == nil {
		return "", errors.New("recorder disabled")
	}
	man, err := h.Manifest(sessionID)
	if err != nil {
		return "", err
	}
	if man.Active {
		return "", errors.New("session still recording; stop first")
	}
	sessDir := filepath.Join(h.dir, sessionID)

	var vidTrack, audTrack *TrackManifest
	for _, t := range man.Tracks {
		// pula gravadores vazios (sem frames)
		if t.Frames == 0 {
			continue
		}
		switch t.Codec {
		case "vp8":
			if vidTrack == nil || t.Frames > vidTrack.Frames {
				vidTrack = t
			}
		case "opus":
			if audTrack == nil || t.Frames > audTrack.Frames {
				audTrack = t
			}
		}
	}
	if vidTrack == nil && audTrack == nil {
		return "", errors.New("no recorded tracks")
	}

	outPath := filepath.Join(sessDir, "mixed.webm")
	out, err := os.Create(outPath)
	if err != nil {
		return "", err
	}
	defer out.Close()

	var (
		vw, vh   uint16
		opusHead []byte
		ch       uint8 = 2
	)
	if vidTrack != nil {
		vw, vh = vidTrack.Width, vidTrack.Height
	}
	if audTrack != nil {
		// OpusHead mínimo (mesmo do nosso OggWriter).
		ch = 2
		opusHead = buildOpusHead(ch, 48000)
	}

	ww, err := NewWebMWriter(out, vidTrack != nil, vw, vh, audTrack != nil, opusHead, ch)
	if err != nil {
		return "", err
	}

	// Etapa 19: offsets persistidos no manifesto definem onde cada trilha
	// começa na timeline real. Normalizamos pra que o min vire 0 (player
	// não engole timestamp negativo) e somamos em cada bloco.
	var vidOff, audOff int64
	if vidTrack != nil {
		vidOff = vidTrack.StartOffsetMs
	}
	if audTrack != nil {
		audOff = audTrack.StartOffsetMs
	}
	base := vidOff
	if audTrack != nil && (vidTrack == nil || audOff < base) {
		base = audOff
	}
	vidOff -= base
	audOff -= base

	// Carrega todos os blocos e ordena por timestamp pra cluster scheduling
	// limpo (o WebM permite intercalar; player remonta).
	type block struct {
		ts       int64
		track    uint8
		keyframe bool
		data     []byte
	}
	var blocks []block

	if vidTrack != nil {
		f, err := os.Open(filepath.Join(sessDir, vidTrack.File))
		if err != nil {
			return "", err
		}
		ivf, err := OpenIVF(f)
		if err != nil {
			f.Close()
			return "", err
		}
		for {
			fr, err := ivf.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				f.Close()
				return "", err
			}
			blocks = append(blocks, block{
				ts: ivf.TimestampMs(fr.PTS) + vidOff, track: trackVideo,
				keyframe: IsVP8Keyframe(fr.Data), data: fr.Data,
			})
		}
		f.Close()
	}

	if audTrack != nil {
		f, err := os.Open(filepath.Join(sessDir, audTrack.File))
		if err != nil {
			return "", err
		}
		og := OpenOgg(f)
		// pula os 2 primeiros pacotes: OpusHead e OpusTags
		skipped := 0
		var prevGranule uint64
		for {
			p, err := og.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				f.Close()
				return "", err
			}
			if skipped < 2 {
				skipped++
				continue
			}
			// timestamp do início do pacote = granule_anterior em ms.
			tsMs := int64(prevGranule) * 1000 / 48000
			prevGranule = p.GranulePosEnd
			if len(p.Data) == 0 {
				continue
			}
			blocks = append(blocks, block{ts: tsMs + audOff, track: trackAudio, keyframe: true, data: p.Data})
		}
		f.Close()
	}

	sort.SliceStable(blocks, func(i, j int) bool { return blocks[i].ts < blocks[j].ts })

	for _, b := range blocks {
		if err := ww.WriteFrame(b.track, b.ts, b.keyframe, b.data); err != nil {
			return "", fmt.Errorf("write block: %w", err)
		}
	}
	if err := ww.Close(); err != nil {
		return "", err
	}
	return outPath, nil
}

func buildOpusHead(channels uint8, sampleRate uint32) []byte {
	h := make([]byte, 19)
	copy(h[0:8], "OpusHead")
	h[8] = 1
	h[9] = channels
	// pre-skip = 0
	h[12] = byte(sampleRate)
	h[13] = byte(sampleRate >> 8)
	h[14] = byte(sampleRate >> 16)
	h[15] = byte(sampleRate >> 24)
	// output gain = 0, channel mapping family = 0
	return h
}
