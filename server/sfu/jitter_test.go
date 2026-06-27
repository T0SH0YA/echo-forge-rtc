package main

import (
	"sort"
	"sync"
	"testing"
	"time"
)

func mkHdr(seq uint16) *RTPHeader {
	return &RTPHeader{SequenceNumber: seq, SSRC: 0xCAFE, HeaderLen: 12}
}

// Pacotes fora de ordem devem sair em ordem após o targetDelay.
func TestJitterReorder(t *testing.T) {
	var mu sync.Mutex
	var got []uint16
	jb := NewJitterBuffer(0xCAFE, func(h *RTPHeader, _ []byte) {
		mu.Lock()
		got = append(got, h.SequenceNumber)
		mu.Unlock()
	}, nil)
	defer jb.Close()

	// 100, 102, 101 — fora de ordem mas sem perda.
	jb.Push(mkHdr(100), []byte{0})
	jb.Push(mkHdr(102), []byte{0})
	jb.Push(mkHdr(101), []byte{0})

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 3 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 3 {
		t.Fatalf("esperava 3 pacotes, got %d (%v)", len(got), got)
	}
	if !sort.SliceIsSorted(got, func(i, j int) bool { return got[i] < got[j] }) {
		t.Fatalf("saída fora de ordem: %v", got)
	}
}

// Gap real deve disparar NACK contendo as seqs faltantes.
func TestJitterNACKOnGap(t *testing.T) {
	var mu sync.Mutex
	var nacked []uint16
	jb := NewJitterBuffer(0xCAFE, func(*RTPHeader, []byte) {}, func(ssrc uint32, lost []uint16) {
		if ssrc != 0xCAFE {
			t.Errorf("ssrc errado: %x", ssrc)
		}
		mu.Lock()
		nacked = append(nacked, lost...)
		mu.Unlock()
	})
	defer jb.Close()

	// Recebe 100 e 103; 101 e 102 ficam faltando.
	jb.Push(mkHdr(100), []byte{0})
	jb.Push(mkHdr(103), []byte{0})

	deadline := time.Now().Add(800 * time.Millisecond)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(nacked)
		mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(nacked) < 2 {
		t.Fatalf("esperava NACK pra 101 e 102, got %v", nacked)
	}
	has := map[uint16]bool{}
	for _, s := range nacked {
		has[s] = true
	}
	if !has[101] || !has[102] {
		t.Fatalf("NACK não cobriu 101/102: %v", nacked)
	}
}

// Pacote duplicado ou já entregue deve ser ignorado.
func TestJitterDropLateAndDup(t *testing.T) {
	count := 0
	jb := NewJitterBuffer(0xCAFE, func(*RTPHeader, []byte) { count++ }, nil)
	defer jb.Close()
	jb.Push(mkHdr(50), []byte{0})
	jb.Push(mkHdr(50), []byte{0}) // dup
	// Espera maturar e emitir.
	time.Sleep(80 * time.Millisecond)
	// Tarde demais: 49 (antes do head).
	jb.Push(mkHdr(49), []byte{0})
	time.Sleep(80 * time.Millisecond)
	if count != 1 {
		t.Fatalf("esperava 1 emissão, got %d", count)
	}
	if jbDup.Load() == 0 {
		t.Fatalf("esperava contador de dup > 0")
	}
}
