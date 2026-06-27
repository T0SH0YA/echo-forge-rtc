// SRTP AES-128-GCM — RFC 7714.
//
// Layout do pacote SRTP-GCM:
//
//	[ RTP header (AAD) ][ ciphertext (= encrypted payload) ][ 16-byte auth tag ]
//
// IV (12 bytes, RFC 7714 §8.1):
//
//	IV = salt XOR ( 00 00 || SSRC(4) || ROC(4) || SEQ(2) )
//
// ROC (rollover counter) é per-SSRC, per-direção. Detectamos wrap quando o
// SEQ "anda pra trás" cruzando o meio do espaço de 16 bits.
//
// Cada sessão tem dois contextos: um pra receber (ClientKey/ClientSalt) e
// outro pra enviar (ServerKey/ServerSalt). Forwarding 1→N decifra com o
// contexto do publisher e re-cifra com o contexto de cada subscriber.
//
// Profile suportado nesta etapa: SRTP_AEAD_AES_128_GCM (key=16, salt=12).
// AES-CM + HMAC-SHA1 (RFC 3711) fica pra depois — GCM é o caminho moderno
// que Chrome/Firefox preferem desde 2019.
package main

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
)

type SRTPContext struct {
	aead cipher.AEAD
	salt []byte // 12 bytes

	mu      sync.Mutex
	roc     map[uint32]uint32 // SSRC → rollover counter
	lastSeq map[uint32]uint16 // SSRC → último SEQ visto (pra detectar wrap)
	seenAny map[uint32]bool
}

func NewSRTPContext(key, salt []byte) (*SRTPContext, error) {
	if len(key) != 16 {
		return nil, fmt.Errorf("srtp: key must be 16 bytes, got %d", len(key))
	}
	if len(salt) != 12 {
		return nil, fmt.Errorf("srtp: salt must be 12 bytes, got %d", len(salt))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &SRTPContext{
		aead:    aead,
		salt:    append([]byte(nil), salt...),
		roc:     map[uint32]uint32{},
		lastSeq: map[uint32]uint16{},
		seenAny: map[uint32]bool{},
	}, nil
}

func (c *SRTPContext) buildIV(ssrc, roc uint32, seq uint16) []byte {
	iv := make([]byte, 12)
	binary.BigEndian.PutUint32(iv[2:6], ssrc)
	binary.BigEndian.PutUint32(iv[6:10], roc)
	binary.BigEndian.PutUint16(iv[10:12], seq)
	for i := 0; i < 12; i++ {
		iv[i] ^= c.salt[i]
	}
	return iv
}

// rocFor: estima o ROC pra este SEQ. Atualiza estado interno.
// Heurística simples: se nunca vimos esse SSRC, começa ROC=0.
// Se SEQ anterior estava acima de 0xC000 e novo SEQ caiu pra abaixo de
// 0x4000 → wrap detectado, ROC++. Caso oposto (out-of-order cruzando wrap)
// usa ROC-1 sem atualizar estado.
func (c *SRTPContext) rocFor(ssrc uint32, seq uint16) uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.seenAny[ssrc] {
		c.seenAny[ssrc] = true
		c.roc[ssrc] = 0
		c.lastSeq[ssrc] = seq
		return 0
	}
	last := c.lastSeq[ssrc]
	roc := c.roc[ssrc]
	if last > 0xC000 && seq < 0x4000 {
		roc++
		c.roc[ssrc] = roc
		c.lastSeq[ssrc] = seq
		return roc
	}
	if last < 0x4000 && seq > 0xC000 {
		// reordenado cruzando wrap pra trás — não muda estado.
		if roc == 0 {
			return 0
		}
		return roc - 1
	}
	if seq > last {
		c.lastSeq[ssrc] = seq
	}
	return roc
}

// Encrypt: pkt = RTP completo (header + payload em claro). Devolve
// header || ciphertext || tag. headerLen vem de ParseRTP.
func (c *SRTPContext) Encrypt(pkt []byte, headerLen int, ssrc uint32, seq uint16) ([]byte, error) {
	if headerLen < 12 || headerLen > len(pkt) {
		return nil, errors.New("srtp: bad header len")
	}
	roc := c.rocFor(ssrc, seq)
	iv := c.buildIV(ssrc, roc, seq)
	header := pkt[:headerLen]
	payload := pkt[headerLen:]
	out := make([]byte, 0, headerLen+len(payload)+c.aead.Overhead())
	out = append(out, header...)
	return c.aead.Seal(out, iv, payload, header), nil
}

// Decrypt: pkt = SRTP completo. Devolve header || plaintext payload.
func (c *SRTPContext) Decrypt(pkt []byte, headerLen int, ssrc uint32, seq uint16) ([]byte, error) {
	if headerLen < 12 || headerLen+c.aead.Overhead() > len(pkt) {
		return nil, errors.New("srtp: bad packet len")
	}
	roc := c.rocFor(ssrc, seq)
	iv := c.buildIV(ssrc, roc, seq)
	header := pkt[:headerLen]
	body := pkt[headerLen:]
	plain, err := c.aead.Open(nil, iv, body, header)
	if err != nil {
		return nil, fmt.Errorf("srtp: %w", err)
	}
	out := make([]byte, 0, headerLen+len(plain))
	out = append(out, header...)
	out = append(out, plain...)
	return out, nil
}
