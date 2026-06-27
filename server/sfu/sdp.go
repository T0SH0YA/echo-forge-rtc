// SDP mínimo pra negociação WebRTC. Parser tolerante (só extrai o que
// usamos) e gerador que monta um answer ICE-lite com candidato host UDP.
//
// Não tentamos cobrir o RFC inteiro. Para Etapa 5 (até DTLS), o cliente
// envia uma offer típica do browser; nós extraímos:
//   - ice-ufrag / ice-pwd remotos
//   - fingerprint remoto (será usado na Etapa 6)
//   - setup (active/passive/actpass)
//   - mids
// E geramos um answer com a mesma estrutura de m-lines + nossos parâmetros.
package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
)

type Media struct {
	Kind        string // "audio" | "video" | "application"
	Mid         string
	Protocol    string   // ex: "UDP/TLS/RTP/SAVPF"
	Fmts        []string // payload types
	Direction   string   // "sendrecv" | "recvonly" | "sendonly" | "inactive"
	Setup       string
	IceUfrag    string
	IcePwd      string
	Fingerprint string
	RIDs        []string // a=rid:<id> send … (publisher anuncia camadas)
	Simulcast   string   // valor cru de a=simulcast (ex: "send q;h;f")
	RIDExtID    uint8    // ID extmap pra urn:ietf:…:rtp-stream-id (0 = ausente)
	RRIDExtID   uint8    // repaired-rtp-stream-id (RTX)
	TWCCExtID   uint8    // transport-wide-cc seq (draft-holmer-01)
	Rtpmap      map[uint8]string // PT → nome de codec lowercase ("vp8","opus","h264","rtx")
	ClockRate   map[uint8]uint32 // PT → clock rate (Hz)
	Extra       []string // linhas a:* que devolvemos verbatim
}






type SessionDesc struct {
	Origin      string
	SessionName string
	IceUfrag    string
	IcePwd      string
	Fingerprint string
	BundleMids  []string
	Media       []Media
}

// ParseOffer faz um parse line-based básico.
func ParseOffer(sdp string) (*SessionDesc, error) {
	s := &SessionDesc{}
	var cur *Media
	flush := func() {
		if cur != nil {
			s.Media = append(s.Media, *cur)
			cur = nil
		}
	}
	for _, line := range strings.Split(sdp, "\n") {
		line = strings.TrimRight(line, "\r")
		if len(line) < 2 || line[1] != '=' {
			continue
		}
		k, v := line[0], line[2:]
		switch k {
		case 'o':
			s.Origin = v
		case 's':
			s.SessionName = v
		case 'm':
			flush()
			parts := strings.Fields(v)
			if len(parts) < 4 {
				return nil, fmt.Errorf("sdp: bad m-line %q", v)
			}
			cur = &Media{Kind: parts[0], Protocol: parts[2], Fmts: parts[3:]}
		case 'a':
			parseAttr(s, cur, v)
		}
	}
	flush()
	return s, nil
}

func parseAttr(s *SessionDesc, m *Media, v string) {
	name, val, _ := strings.Cut(v, ":")
	switch name {
	case "group":
		if strings.HasPrefix(val, "BUNDLE ") {
			s.BundleMids = strings.Fields(strings.TrimPrefix(val, "BUNDLE "))
		}
	case "ice-ufrag":
		if m != nil {
			m.IceUfrag = val
		}
		if s.IceUfrag == "" {
			s.IceUfrag = val
		}
	case "ice-pwd":
		if m != nil {
			m.IcePwd = val
		}
		if s.IcePwd == "" {
			s.IcePwd = val
		}
	case "fingerprint":
		if m != nil {
			m.Fingerprint = val
		}
		if s.Fingerprint == "" {
			s.Fingerprint = val
		}
	case "setup":
		if m != nil {
			m.Setup = val
		}
	case "mid":
		if m != nil {
			m.Mid = val
		}
	case "sendrecv", "recvonly", "sendonly", "inactive":
		if m != nil {
			m.Direction = name
		}
	case "rid":
		if m != nil {
			// "rid:<id> <direction> [restrictions]" — só guardamos os IDs
			if id, _, ok := strings.Cut(val, " "); ok {
				m.RIDs = append(m.RIDs, id)
			} else {
				m.RIDs = append(m.RIDs, val)
			}
		}
	case "simulcast":
		if m != nil {
			m.Simulcast = val
		}
	case "extmap":
		if m != nil {
			// "extmap:<id>[/dir] <uri>"
			idPart, uri, ok := strings.Cut(val, " ")
			if ok {
				if slash := strings.IndexByte(idPart, '/'); slash > 0 {
					idPart = idPart[:slash]
				}
				var id uint8
				for _, c := range idPart {
					if c < '0' || c > '9' {
						id = 0
						break
					}
					id = id*10 + uint8(c-'0')
				}
				uri = strings.TrimSpace(uri)
				switch uri {
				case RIDExtURI:
					m.RIDExtID = id
				case RepairExtURI:
					m.RRIDExtID = id
				case TWCCExtURI:
					m.TWCCExtID = id
				}

			}
			m.Extra = append(m.Extra, v)
		}
	default:
		if m != nil {
			m.Extra = append(m.Extra, v)
		}
	}
}


// AnswerParams: o que o servidor anuncia.
type AnswerParams struct {
	IceUfrag    string
	IcePwd      string
	Fingerprint string  // "sha-256 AA:BB:..." — preenchido na Etapa 6
	HostIP      string
	HostPort    int
}

// BuildAnswer gera um SDP answer ICE-lite com um único candidato host UDP.
// Todas as m-lines compartilham o mesmo transporte (BUNDLE).
func BuildAnswer(offer *SessionDesc, p AnswerParams) string {
	var b strings.Builder
	b.WriteString("v=0\r\n")
	fmt.Fprintf(&b, "o=- %d 2 IN IP4 0.0.0.0\r\n", randUint64())
	b.WriteString("s=-\r\n")
	b.WriteString("t=0 0\r\n")
	if len(offer.BundleMids) > 0 {
		fmt.Fprintf(&b, "a=group:BUNDLE %s\r\n", strings.Join(offer.BundleMids, " "))
	}
	b.WriteString("a=ice-lite\r\n")
	b.WriteString("a=msid-semantic: WMS *\r\n")

	for i, m := range offer.Media {
		// Reusa todos os fmts do offer (não negociamos codecs nesta etapa).
		port := p.HostPort
		if i > 0 {
			port = 9 // BUNDLE: só a primeira m-line tem porta real
		}
		fmt.Fprintf(&b, "m=%s %d %s %s\r\n", m.Kind, port, m.Protocol, strings.Join(m.Fmts, " "))
		fmt.Fprintf(&b, "c=IN IP4 %s\r\n", p.HostIP)
		b.WriteString("a=rtcp-mux\r\n")
		fmt.Fprintf(&b, "a=ice-ufrag:%s\r\n", p.IceUfrag)
		fmt.Fprintf(&b, "a=ice-pwd:%s\r\n", p.IcePwd)
		fmt.Fprintf(&b, "a=fingerprint:%s\r\n", p.Fingerprint)
		// Se a offer pediu actpass/active, respondemos passive (servidor aceita conexão).
		setup := "passive"
		if m.Setup == "passive" {
			setup = "active"
		}
		fmt.Fprintf(&b, "a=setup:%s\r\n", setup)
		fmt.Fprintf(&b, "a=mid:%s\r\n", m.Mid)
		dir := m.Direction
		switch dir {
		case "sendonly":
			dir = "recvonly"
		case "recvonly":
			dir = "sendonly"
		case "":
			dir = "sendrecv"
		}
		fmt.Fprintf(&b, "a=%s\r\n", dir)
		// Devolve atributos relevantes (rtpmap/fmtp/rtcp-fb) do offer pra manter codecs.
		for _, ex := range m.Extra {
			if startsWithAny(ex, "rtpmap:", "fmtp:", "rtcp-fb:", "extmap:") {
				fmt.Fprintf(&b, "a=%s\r\n", ex)
			}
		}
		// Espelhamos rid/simulcast: publisher manda send q;h;f → answer
		// devolve recv q;h;f e a=rid:<id> recv pra cada camada.
		for _, rid := range m.RIDs {
			fmt.Fprintf(&b, "a=rid:%s recv\r\n", rid)
		}
		if m.Simulcast != "" {
			// "send q;h;f" → "recv q;h;f"
			sc := m.Simulcast
			if strings.HasPrefix(sc, "send ") {
				sc = "recv " + strings.TrimPrefix(sc, "send ")
			}
			fmt.Fprintf(&b, "a=simulcast:%s\r\n", sc)
		}
		// Candidato host único (UDP).
		fmt.Fprintf(&b, "a=candidate:1 1 UDP 2130706431 %s %d typ host\r\n", p.HostIP, p.HostPort)
		b.WriteString("a=end-of-candidates\r\n")
	}
	return b.String()
}


func startsWithAny(s string, ps ...string) bool {
	for _, p := range ps {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// RandomUfrag/Pwd geram credenciais ICE conforme RFC 8445 §5.3 (chars
// ice-char, ufrag ≥4, pwd ≥22).
func RandomUfrag() string { return randBase64(6) }
func RandomPwd() string   { return randBase64(24) }

func randBase64(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	s := base64.RawURLEncoding.EncodeToString(b)
	return s
}

func randUint64() uint64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return uint64(b[0])<<56 | uint64(b[1])<<48 | uint64(b[2])<<40 | uint64(b[3])<<32 |
		uint64(b[4])<<24 | uint64(b[5])<<16 | uint64(b[6])<<8 | uint64(b[7])
}
