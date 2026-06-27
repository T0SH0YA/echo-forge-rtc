// Allocation: estado por 5-tupla do cliente. Cada allocation tem um socket
// UDP "relay" próprio (porta efêmera) que faz ponte entre o cliente TURN e
// peers externos.
package main

import (
	"encoding/binary"
	"log"
	"net"
	"sync"
	"time"
)

const (
	defaultLifetime = 600 * time.Second
	maxLifetime     = 3600 * time.Second
	permissionTTL   = 5 * time.Minute
	channelTTL      = 10 * time.Minute

	channelMin uint16 = 0x4000
	channelMax uint16 = 0x7FFE
)

type permission struct {
	ip      net.IP
	expires time.Time
}

type channel struct {
	number  uint16
	peer    *net.UDPAddr
	expires time.Time
}

// Allocation = um cliente autorizado com um relay socket dedicado.
type Allocation struct {
	clientAddr  *net.UDPAddr // 5-tupla do cliente TURN
	username    string
	key         []byte // long-term key pro MESSAGE-INTEGRITY
	relayConn   *net.UDPConn
	relayAddr   *net.UDPAddr // endereço público do relay (anunciado pro cliente)
	expires     time.Time

	mu          sync.Mutex
	permissions map[string]*permission // key = ip.String()
	channels    map[uint16]*channel    // by number
	chanByPeer  map[string]uint16      // key = "ip:port"
	closed      bool
}

func (a *Allocation) close() {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	a.closed = true
	a.mu.Unlock()
	_ = a.relayConn.Close()
}

func (a *Allocation) hasPermission(ip net.IP) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, ok := a.permissions[ip.String()]
	if !ok || time.Now().After(p.expires) {
		return false
	}
	return true
}

func (a *Allocation) addPermission(ip net.IP) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.permissions[ip.String()] = &permission{ip: ip, expires: time.Now().Add(permissionTTL)}
}

func (a *Allocation) bindChannel(num uint16, peer *net.UDPAddr) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	key := peer.String()
	if ex, ok := a.channels[num]; ok {
		if ex.peer.String() != key {
			return errChannelInUse
		}
	}
	if other, ok := a.chanByPeer[key]; ok && other != num {
		return errChannelInUse
	}
	a.channels[num] = &channel{number: num, peer: peer, expires: time.Now().Add(channelTTL)}
	a.chanByPeer[key] = num
	// Channel bind implicitamente cria permissão.
	a.permissions[peer.IP.String()] = &permission{ip: peer.IP, expires: time.Now().Add(permissionTTL)}
	return nil
}

func (a *Allocation) channelFor(peer *net.UDPAddr) (uint16, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	num, ok := a.chanByPeer[peer.String()]
	if !ok {
		return 0, false
	}
	ch := a.channels[num]
	if time.Now().After(ch.expires) {
		return 0, false
	}
	return num, true
}

func (a *Allocation) peerForChannel(num uint16) (*net.UDPAddr, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	ch, ok := a.channels[num]
	if !ok || time.Now().After(ch.expires) {
		return nil, false
	}
	return ch.peer, true
}

func (a *Allocation) refresh(lifetime time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.expires = time.Now().Add(lifetime)
}

// ---- Manager ----

type allocManager struct {
	mu          sync.Mutex
	allocs      map[string]*Allocation // key = client.String()
	publicIP    net.IP                 // IP anunciado nos XOR-RELAYED-ADDRESS
	relayListen string                 // host pros novos relay sockets ("0.0.0.0")
}

func newAllocManager(publicIP net.IP, listenHost string) *allocManager {
	return &allocManager{
		allocs:      make(map[string]*Allocation),
		publicIP:    publicIP,
		relayListen: listenHost,
	}
}

func (m *allocManager) get(client *net.UDPAddr) *Allocation {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.allocs[client.String()]
}

// create abre um novo socket UDP de relay para o cliente.
func (m *allocManager) create(client *net.UDPAddr, username string, key []byte, lifetime time.Duration, srv *Server) (*Allocation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.allocs[client.String()]; ok {
		return nil, errAllocationMismatch
	}
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(m.relayListen), Port: 0})
	if err != nil {
		return nil, err
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	a := &Allocation{
		clientAddr:  client,
		username:    username,
		key:         key,
		relayConn:   conn,
		relayAddr:   &net.UDPAddr{IP: m.publicIP, Port: port},
		expires:     time.Now().Add(lifetime),
		permissions: make(map[string]*permission),
		channels:    make(map[uint16]*channel),
		chanByPeer:  make(map[string]uint16),
	}
	m.allocs[client.String()] = a
	go srv.relayReadLoop(a)
	return a, nil
}

func (m *allocManager) remove(client *net.UDPAddr) {
	m.mu.Lock()
	a, ok := m.allocs[client.String()]
	if ok {
		delete(m.allocs, client.String())
	}
	m.mu.Unlock()
	if a != nil {
		a.close()
	}
}

// gc remove allocations expiradas.
func (m *allocManager) gc() {
	now := time.Now()
	m.mu.Lock()
	expired := []*Allocation{}
	for k, a := range m.allocs {
		a.mu.Lock()
		exp := a.expires
		a.mu.Unlock()
		if now.After(exp) {
			expired = append(expired, a)
			delete(m.allocs, k)
		}
	}
	m.mu.Unlock()
	for _, a := range expired {
		log.Printf("[turn] alloc expired client=%s relay=%s", a.clientAddr, a.relayAddr)
		a.close()
	}
}

// --- ChannelData framing ---

func encodeChannelData(num uint16, data []byte) []byte {
	out := make([]byte, 4+len(data))
	binary.BigEndian.PutUint16(out[0:2], num)
	binary.BigEndian.PutUint16(out[2:4], uint16(len(data)))
	copy(out[4:], data)
	// Sobre UDP não exige padding até 4 bytes (RFC 5766 §11.5), mas alguns
	// stacks esperam. Adicionamos pra segurança.
	if pad := (4 - len(data)%4) % 4; pad > 0 {
		out = append(out, make([]byte, pad)...)
	}
	return out
}

func decodeChannelData(b []byte) (uint16, []byte, bool) {
	if len(b) < 4 {
		return 0, nil, false
	}
	num := binary.BigEndian.Uint16(b[0:2])
	if num < channelMin || num > channelMax {
		return 0, nil, false
	}
	l := binary.BigEndian.Uint16(b[2:4])
	if int(l)+4 > len(b) {
		return 0, nil, false
	}
	return num, b[4 : 4+l], true
}
