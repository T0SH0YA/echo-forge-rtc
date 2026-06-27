// Etapa 6 — DTLS por sessão.
//
// Cada Session gera um par cert ECDSA P-256 self-signed na hora da negociação.
// O fingerprint SHA-256 do cert (formato "sha-256 AA:BB:...") vai no a=fingerprint
// do answer SDP. Quando o ICE conecta, disparamos o handshake DTLS no papel de
// servidor (setup:passive) e validamos que o cert que o cliente apresentou bate
// com o fingerprint anunciado no offer (defesa contra MITM dentro do túnel ICE).
//
// Depois do handshake, extraímos keying material via RFC 5705 com o label
// "EXTRACTOR-dtls_srtp" — exatamente o que SRTP-DTLS (RFC 5764) precisa pra
// derivar as chaves SRTP que a Etapa 7 vai usar.
//
// Engine: pion/dtls (MIT). Tudo que é específico do nosso fluxo —
// geração/serialização do cert, fingerprint, verificação cruzada de fingerprint,
// extração de keying material — é código nosso aqui.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"time"

	dtls "github.com/pion/dtls/v2"
)

// SRTP profile que negociamos com o peer. AES-128-GCM = RFC 7714, é o que vamos
// implementar na Etapa 7.
var srtpProfiles = []dtls.SRTPProtectionProfile{
	dtls.SRTP_AEAD_AES_128_GCM,
	dtls.SRTP_AES128_CM_HMAC_SHA1_80, // fallback amplamente suportado
}

// dtlsKeyingMaterialLabel — RFC 5764 §4.2.
const dtlsKeyingMaterialLabel = "EXTRACTOR-dtls_srtp"

// generateDTLSCert cria um par ECDSA P-256 + cert self-signed válido por 30d.
// Sujeito é irrelevante (DTLS-SRTP usa fingerprint, não CA).
func generateDTLSCert() (*dtls.Certificate, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("ecdsa: %w", err)
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "webrtc-own-sfu"},
		NotBefore:             time.Now().Add(-1 * time.Minute),
		NotAfter:              time.Now().Add(30 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, fmt.Errorf("x509: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &dtls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  priv,
		Leaf:        cert,
	}, nil
}

// FingerprintSHA256 devolve "sha-256 AA:BB:..." pro a=fingerprint do SDP
// (RFC 8122 §5). Cert é DER do leaf.
func FingerprintSHA256(certDER []byte) string {
	sum := sha256.Sum256(certDER)
	hex := strings.ToUpper(hex.EncodeToString(sum[:]))
	var b strings.Builder
	b.WriteString("sha-256 ")
	for i := 0; i < len(hex); i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(hex[i : i+2])
	}
	return b.String()
}

// matchFingerprint compara o fingerprint declarado no SDP do peer contra o
// cert que ele apresentou no handshake. Normaliza pra evitar falso negativo
// por case/whitespace. Suporta sha-256 por enquanto (o que Chrome/Firefox
// usam por padrão).
func matchFingerprint(declared string, certDER []byte) error {
	parts := strings.Fields(declared)
	if len(parts) != 2 {
		return fmt.Errorf("fingerprint malformed: %q", declared)
	}
	algo := strings.ToLower(parts[0])
	if algo != "sha-256" {
		return fmt.Errorf("fingerprint algo %q not supported", algo)
	}
	want := strings.ToUpper(strings.ReplaceAll(parts[1], ":", ""))
	sum := sha256.Sum256(certDER)
	got := strings.ToUpper(hex.EncodeToString(sum[:]))
	if want != got {
		return fmt.Errorf("fingerprint mismatch: want=%s got=%s", want, got)
	}
	return nil
}

// SRTPKeyingMaterial — chaves derivadas pós-handshake, prontas pra SRTP.
// Layout RFC 5764 §4.2 + RFC 5705. Tamanhos dependem do profile negociado.
type SRTPKeyingMaterial struct {
	Profile      dtls.SRTPProtectionProfile
	ClientKey    []byte
	ServerKey    []byte
	ClientSalt   []byte
	ServerSalt   []byte
}

// keyAndSaltLen devolve (keyLen, saltLen) do profile. Não dá pra perguntar pro
// pion sem pegar API interna, então mantemos um switch pequeno.
func keyAndSaltLen(p dtls.SRTPProtectionProfile) (int, int, error) {
	switch p {
	case dtls.SRTP_AEAD_AES_128_GCM:
		return 16, 12, nil
	case dtls.SRTP_AES128_CM_HMAC_SHA1_80, dtls.SRTP_AES128_CM_HMAC_SHA1_32:
		return 16, 14, nil
	default:
		return 0, 0, fmt.Errorf("srtp profile 0x%04x not supported", p)
	}
}

// extractSRTPKeys roda EKM e fatia conforme RFC 5764.
func extractSRTPKeys(conn *dtls.Conn) (*SRTPKeyingMaterial, error) {
	state, ok := conn.ConnectionState().ExportKeyingMaterial, conn.ConnectionState().SRTPProtectionProfile
	if state == nil {
		return nil, fmt.Errorf("dtls: ExportKeyingMaterial missing")
	}
	profile := dtls.SRTPProtectionProfile(ok)
	keyLen, saltLen, err := keyAndSaltLen(profile)
	if err != nil {
		return nil, err
	}
	total := 2*keyLen + 2*saltLen
	material, err := state(dtlsKeyingMaterialLabel, nil, total)
	if err != nil {
		return nil, fmt.Errorf("ekm: %w", err)
	}
	out := &SRTPKeyingMaterial{Profile: profile}
	off := 0
	out.ClientKey = append([]byte(nil), material[off:off+keyLen]...)
	off += keyLen
	out.ServerKey = append([]byte(nil), material[off:off+keyLen]...)
	off += keyLen
	out.ClientSalt = append([]byte(nil), material[off:off+saltLen]...)
	off += saltLen
	out.ServerSalt = append([]byte(nil), material[off:off+saltLen]...)
	return out, nil
}

// runDTLSServer toca o handshake como servidor (cliente é o browser/peer).
// A net.Conn é o pipe alimentado pelo udpLoop quando demuxar pacotes DTLS
// pra essa sessão. Bloqueia até o handshake terminar (sucesso ou erro).
func runDTLSServer(ctx context.Context, pipe *dtlsPacketConn, cert *dtls.Certificate, remoteFingerprint string) (*dtls.Conn, *SRTPKeyingMaterial, error) {
	cfg := &dtls.Config{
		Certificates:           []dtls.Certificate{*cert},
		SRTPProtectionProfiles: srtpProfiles,
		ClientAuth:             dtls.RequireAnyClientCert,
		ExtendedMasterSecret:   dtls.RequireExtendedMasterSecret,
		// Validação real do fingerprint é nossa, abaixo. A função aqui evita que
		// o pion rejeite o cert por "unknown CA".
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("dtls: peer sent no cert")
			}
			return matchFingerprint(remoteFingerprint, rawCerts[0])
		},
	}
	hsCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, err := dtls.ServerWithContext(hsCtx, pipe, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("dtls handshake: %w", err)
	}
	keys, err := extractSRTPKeys(conn)
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	return conn, keys, nil
}
