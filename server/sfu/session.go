// Session = uma conexão WebRTC em negociação/ativa contra um peer.
// O agente ICE-lite indexa sessões por nosso ice-ufrag local; o USERNAME
// do STUN binding chega como "localUfrag:remoteUfrag", então conseguimos
// achar a sessão antes mesmo de saber o endereço UDP do peer.
package main

import (
	"strings"
	"sync"
	"time"

	"crypto/tls"

	dtls "github.com/pion/dtls/v2"
)

type ICEState int

const (
	ICENew ICEState = iota
	ICEChecking
	ICEConnected
)

type DTLSState int

const (
	DTLSIdle DTLSState = iota
	DTLSHandshaking
	DTLSEstablished
	DTLSFailed
)

func (s ICEState) String() string {
	switch s {
	case ICEChecking:
		return "checking"
	case ICEConnected:
		return "connected"
	default:
		return "new"
	}
}

type Session struct {
	ID           string
	LocalUfrag   string
	LocalPwd     string
	RemoteUfrag  string
	RemotePwd    string
	RemoteFinger string // "sha-256 AA:BB:..." da offer

	// DTLS por sessão (Etapa 6)
	LocalCert        *tls.Certificate
	LocalFingerprint string // "sha-256 AA:BB:..." do nosso cert
	dtlsPipe         *dtlsPacketConn
	dtlsConn         *dtls.Conn
	dtlsState        DTLSState
	srtpKeys         *SRTPKeyingMaterial
	srtpRecv         *SRTPContext  // decifra RTP do peer (ClientKey/ClientSalt)
	srtpSend         *SRTPContext  // cifra RTP pro peer (ServerKey/ServerSalt)
	srtcpRecv        *SRTCPContext // decifra RTCP do peer (mesmas chaves SRTP)
	srtcpSend        *SRTCPContext // cifra RTCP pro peer

	publishedSSRCs map[uint32]bool // SSRCs que vimos publicar — mantém set

	// Simulcast (Etapa 10)
	RIDExtID  uint8             // ID extmap pra urn:…:rtp-stream-id (do offer)
	RRIDExtID uint8             // repaired-rtp-stream-id (RTX)
	TWCCExtID uint8             // transport-wide-cc seq (Etapa 13)
	OfferedRIDs []string        // camadas anunciadas em a=rid:<id> send
	layerSSRC map[string]uint32 // rid → ssrc descoberto via header ext
	ssrcLayer map[uint32]string // ssrc → rid (inverso)

	// TWCC + BWE (Etapa 13)
	twcc       *TWCCRecorder
	bwe        *BWE
	lastSeq    map[uint32]uint16 // ssrc → último seq visto (loss tracking)
	rtpSSRC    uint32            // SSRC arbitrário do SFU pra FB/REMB

	// Downstream BWE (Etapa 14) — espaço de twcc seq local pro EGRESSO
	// (este peer é o subscriber recebendo do SFU) e estimador delay-based.
	subBWE     *DownstreamBWE
	subTwccSeq uint16
	// auto-switch: última camada escolhida automaticamente por publisher
	autoLayer  map[string]string


	// Preferência por subscriber: qual rid quero receber de cada publisher.
	// Chave = publisher session ID. "" = layer mais alta disponível.
	prefLayer map[string]string

	// SCTP / DataChannels (Etapa 11)
	sctp *sctpState

	mu           sync.Mutex
	remoteAddr   string // "ip:port" do par nomeado
	state        ICEState
	lastActivity time.Time
	useCandidate bool
	dtlsStarted  bool
}



func (s *Session) markChecking()        { s.mu.Lock(); s.state = ICEChecking; s.lastActivity = time.Now(); s.mu.Unlock() }
func (s *Session) markConnected(a string) {
	s.mu.Lock()
	if s.state != ICEConnected || s.remoteAddr != a {
		s.state = ICEConnected
		s.remoteAddr = a
	}
	s.lastActivity = time.Now()
	s.mu.Unlock()
}
func (s *Session) State() ICEState { s.mu.Lock(); defer s.mu.Unlock(); return s.state }
func (s *Session) RemoteAddr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.remoteAddr
}

// rememberLayer registra rid↔ssrc visto num pacote RTP do publisher.
// Retorna true se foi a primeira vez (caller pode logar/avisar).
func (s *Session) rememberLayer(rid string, ssrc uint32) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.layerSSRC == nil {
		s.layerSSRC = map[string]uint32{}
		s.ssrcLayer = map[uint32]string{}
	}
	if cur, ok := s.layerSSRC[rid]; ok && cur == ssrc {
		return false
	}
	s.layerSSRC[rid] = ssrc
	s.ssrcLayer[ssrc] = rid
	return true
}

func (s *Session) layerOfSSRC(ssrc uint32) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ssrcLayer == nil {
		return ""
	}
	return s.ssrcLayer[ssrc]
}

// availableLayers devolve as camadas (rid) já descobertas, ordenadas por
// rank (menor → maior qualidade) usando LayerRank.
func (s *Session) availableLayers() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	rids := make([]string, 0, len(s.layerSSRC))
	for r := range s.layerSSRC {
		rids = append(rids, r)
	}
	// sort por rank
	for i := 1; i < len(rids); i++ {
		for j := i; j > 0 && LayerRank(rids[j]) < LayerRank(rids[j-1]); j-- {
			rids[j], rids[j-1] = rids[j-1], rids[j]
		}
	}
	return rids
}

// setPrefLayer: este (subscriber) prefere receber `rid` do publisher pubID.
func (s *Session) setPrefLayer(pubID, rid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.prefLayer == nil {
		s.prefLayer = map[string]string{}
	}
	s.prefLayer[pubID] = rid
}

func (s *Session) getPrefLayer(pubID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.prefLayer == nil {
		return ""
	}
	return s.prefLayer[pubID]
}


// SessionStore: lookup por LocalUfrag, ID e endereço remoto.
type SessionStore struct {
	mu   sync.RWMutex
	m    map[string]*Session // localUfrag → session
	id   map[string]*Session // id → session
	addr map[string]*Session // "ip:port" → session (preenchido pós-ICE)
}

func newSessionStore() *SessionStore {
	return &SessionStore{
		m:    map[string]*Session{},
		id:   map[string]*Session{},
		addr: map[string]*Session{},
	}
}

func (st *SessionStore) Add(s *Session) {
	st.mu.Lock()
	st.m[s.LocalUfrag] = s
	st.id[s.ID] = s
	st.mu.Unlock()
}

func (st *SessionStore) ByLocalUfrag(u string) *Session {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.m[u]
}

func (st *SessionStore) ByID(id string) *Session {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.id[id]
}

func (st *SessionStore) ByAddr(a string) *Session {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.addr[a]
}

func (st *SessionStore) BindAddr(a string, s *Session) {
	st.mu.Lock()
	st.addr[a] = s
	st.mu.Unlock()
}

func (st *SessionStore) Remove(id string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if s, ok := st.id[id]; ok {
		delete(st.id, id)
		delete(st.m, s.LocalUfrag)
		if s.remoteAddr != "" {
			delete(st.addr, s.remoteAddr)
		}
	}
}

// splitUsername quebra USERNAME do STUN ICE ("localUfrag:remoteUfrag").
// Retorna (local, remote, ok).
func splitUsername(u string) (string, string, bool) {
	i := strings.IndexByte(u, ':')
	if i <= 0 || i == len(u)-1 {
		return "", "", false
	}
	return u[:i], u[i+1:], true
}
