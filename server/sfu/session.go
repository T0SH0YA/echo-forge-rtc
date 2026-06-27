// Session = uma conexão WebRTC em negociação/ativa contra um peer.
// O agente ICE-lite indexa sessões por nosso ice-ufrag local; o USERNAME
// do STUN binding chega como "localUfrag:remoteUfrag", então conseguimos
// achar a sessão antes mesmo de saber o endereço UDP do peer.
package main

import (
	"strings"
	"sync"
	"time"
)

type ICEState int

const (
	ICENew ICEState = iota
	ICEChecking
	ICEConnected
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
	ID            string
	LocalUfrag    string
	LocalPwd      string
	RemoteUfrag   string
	RemotePwd     string
	RemoteFinger  string // "sha-256 AA:BB:..." da offer
	mu            sync.Mutex
	remoteAddr    string // "ip:port" do par nomeado
	state         ICEState
	lastActivity  time.Time
	useCandidate  bool
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

// SessionStore: lookup por LocalUfrag.
type SessionStore struct {
	mu sync.RWMutex
	m  map[string]*Session // localUfrag → session
	id map[string]*Session // id → session
}

func newSessionStore() *SessionStore {
	return &SessionStore{m: map[string]*Session{}, id: map[string]*Session{}}
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

func (st *SessionStore) Remove(id string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if s, ok := st.id[id]; ok {
		delete(st.id, id)
		delete(st.m, s.LocalUfrag)
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
