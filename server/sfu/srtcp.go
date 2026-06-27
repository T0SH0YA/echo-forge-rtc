// SRTCP AES-128-GCM — RFC 7714 §9.
//
// Layout do pacote SRTCP-GCM (encrypted=1):
//
//	[ RTCP bytes 0..7 plaintext ][ ciphertext(bytes 8..N) ][ 16B tag ][ E||index (4B) ]
//
// Os 8 primeiros bytes do RTCP (header + sender SSRC) ficam em claro — o
// SFU precisa ler o sender SSRC ANTES de decifrar pra saber qual contexto
// usar (o salt do IV depende do SSRC).
//
// IV (12B): bytes 0..1 = 0; 2..5 = SSRC; 6..7 = 0; 8..11 = SRTCP index (E-bit
// limpo). XOR com salt.
//
// AAD: primeiros 8 bytes do RTCP || trailer (E_bit||index, 4B).
//
// SRTCP index é per-contexto-de-envio (não per-SSRC): é um contador
// monotônico de 31 bits que incrementa a cada pacote SRTCP enviado.
// Na recepção, lemos o trailer pra saber o índice (anti-replay fica pra
// depois — aqui só conferimos a auth tag).
//
// Chave/salt: as MESMAS chaves SRTP da direção correspondente (RFC 7714
// §9: SRTP e SRTCP compartilham material derivado).
package main

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"fmt"
	"sync/atomic"
)

const srtcpEBit uint32 = 0x80000000
const srtcpIndexMask uint32 = 0x7FFFFFFF
const srtcpHeaderPlain = 8 // RTCP header(4) + sender SSRC(4)

type SRTCPContext struct {
	aead    cipher.AEAD
	salt    []byte
	txIndex atomic.Uint32 // próximo índice de envio (começa em 1)
}

func NewSRTCPContext(key, salt []byte) (*SRTCPContext, error) {
	if len(key) != 16 {
		return nil, fmt.Errorf("srtcp: key must be 16 bytes, got %d", len(key))
	}
	if len(salt) != 12 {
		return nil, fmt.Errorf("srtcp: salt must be 12 bytes, got %d", len(salt))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &SRTCPContext{aead: aead, salt: append([]byte(nil), salt...)}, nil
}

func (c *SRTCPContext) buildIV(ssrc, index uint32) []byte {
	iv := make([]byte, 12)
	binary.BigEndian.PutUint32(iv[2:6], ssrc)
	binary.BigEndian.PutUint32(iv[8:12], index&srtcpIndexMask)
	for i := 0; i < 12; i++ {
		iv[i] ^= c.salt[i]
	}
	return iv
}

// Encrypt: pkt = RTCP compound completo em claro.
// Devolve [pkt[0..7]][ciphertext][tag][E||index].
func (c *SRTCPContext) Encrypt(pkt []byte) ([]byte, error) {
	if len(pkt) < srtcpHeaderPlain+4 {
		return nil, errors.New("srtcp: short rtcp")
	}
	ssrc := binary.BigEndian.Uint32(pkt[4:8])
	idx := c.txIndex.Add(1) & srtcpIndexMask
	iv := c.buildIV(ssrc, idx)
	header := pkt[:srtcpHeaderPlain]
	body := pkt[srtcpHeaderPlain:]
	trailer := make([]byte, 4)
	binary.BigEndian.PutUint32(trailer, idx|srtcpEBit)
	aad := make([]byte, 0, srtcpHeaderPlain+4)
	aad = append(aad, header...)
	aad = append(aad, trailer...)

	out := make([]byte, 0, len(pkt)+c.aead.Overhead()+4)
	out = append(out, header...)
	out = c.aead.Seal(out, iv, body, aad)
	out = append(out, trailer...)
	return out, nil
}

// Decrypt: pkt = SRTCP completo. Devolve RTCP plaintext.
func (c *SRTCPContext) Decrypt(pkt []byte) ([]byte, error) {
	if len(pkt) < srtcpHeaderPlain+c.aead.Overhead()+4 {
		return nil, errors.New("srtcp: short pkt")
	}
	trailer := pkt[len(pkt)-4:]
	indexWord := binary.BigEndian.Uint32(trailer)
	if indexWord&srtcpEBit == 0 {
		// Não cifrado — autenticação ainda exigida pelo RFC, mas Chrome
		// sempre cifra RTCP quando negocia SRTP-GCM. Recuse.
		return nil, errors.New("srtcp: unencrypted not supported")
	}
	idx := indexWord & srtcpIndexMask
	ssrc := binary.BigEndian.Uint32(pkt[4:8])
	iv := c.buildIV(ssrc, idx)

	header := pkt[:srtcpHeaderPlain]
	encEnd := len(pkt) - 4 // antes do trailer
	body := pkt[srtcpHeaderPlain:encEnd]
	aad := make([]byte, 0, srtcpHeaderPlain+4)
	aad = append(aad, header...)
	aad = append(aad, trailer...)

	plain, err := c.aead.Open(nil, iv, body, aad)
	if err != nil {
		return nil, fmt.Errorf("srtcp: %w", err)
	}
	out := make([]byte, 0, srtcpHeaderPlain+len(plain))
	out = append(out, header...)
	out = append(out, plain...)
	return out, nil
}
