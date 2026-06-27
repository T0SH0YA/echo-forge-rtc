// Egress helpers — manipulação do pacote RTP de saída pra cada subscriber.
//
// Reescrita do twcc seq: cada subscriber tem seu próprio espaço de twcc
// sequence numbers. Sem isso, dois subscribers ouviriam o mesmo seq do
// publisher e os FBs ficariam ambíguos. Reescrevemos o valor de 2 bytes da
// extensão antes do SRTP.Encrypt — como o RTP header inteiro vira AAD,
// alterar bytes no header ANTES da cifragem é seguro: o receiver autentica
// exatamente o que mandamos.
package main

import (
	"encoding/binary"
)

// RewriteOneByteExtValue procura a extensão RFC 8285 one-byte com `id`
// dentro de extData (apenas os bytes APÓS o profile+length header de 4B)
// e sobrescreve o valor com `newVal`. Tamanho deve bater. Retorna true se
// reescreveu.
func RewriteOneByteExtValue(profile uint16, extData []byte, id uint8, newVal []byte) bool {
	if profile != 0xBEDE || id == 0 || id == 15 {
		return false
	}
	off := 0
	for off < len(extData) {
		b := extData[off]
		if b == 0 {
			off++
			continue
		}
		extID := b >> 4
		length := int(b&0x0F) + 1
		off++
		if extID == 15 {
			return false
		}
		if off+length > len(extData) {
			return false
		}
		if extID == id {
			if len(newVal) != length {
				return false
			}
			copy(extData[off:off+length], newVal)
			return true
		}
		off += length
	}
	return false
}

// CloneRTPAndRewriteTWCC clona o pacote (header+payload em claro) e
// reescreve o twcc seq da extensão pro valor `newSeq`. Devolve o clone.
// Se a extensão não estiver presente ou o id for 0, retorna o original
// sem cópia.
func CloneRTPAndRewriteTWCC(plain []byte, hdr *RTPHeader, extID uint8, newSeq uint16) []byte {
	if extID == 0 || !hdr.Extension || len(hdr.ExtensionData) == 0 {
		return plain
	}
	out := make([]byte, len(plain))
	copy(out, plain)
	// localiza extData dentro de out: começa em 12 + 4*CSRCCount + 4.
	extStart := 12 + 4*int(hdr.CSRCCount) + 4
	extEnd := extStart + len(hdr.ExtensionData)
	if extEnd > len(out) {
		return plain
	}
	var buf [2]byte
	binary.BigEndian.PutUint16(buf[:], newSeq)
	if !RewriteOneByteExtValue(hdr.ExtensionProfile, out[extStart:extEnd], extID, buf[:]) {
		return plain // ext não encontrada
	}
	return out
}
