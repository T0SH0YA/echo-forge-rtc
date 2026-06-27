// dtlsPacketConn — net.Conn adapter pra entregar pacotes UDP demuxados pro
// engine DTLS de uma sessão.
//
// O servidor SFU tem UMA única porta UDP recebendo tudo (STUN, DTLS, depois
// SRTP). O demux por primeiro byte (RFC 7983) decide o destino:
//
//	[0..3]    → STUN
//	[20..63]  → DTLS
//	[64..79]  → TURN ChannelData (não aplica aqui)
//	[128..191]→ RTP/RTCP
//
// Pacotes DTLS de uma sessão precisam virar leituras sequenciais pro
// pion/dtls, que espera um net.Conn orientado a datagramas. Esse tipo expõe:
//
//   - Push(b)        : chamado pelo udpLoop quando demuxa um datagrama DTLS
//   - Read/Write/... : usado pelo pion/dtls como net.Conn normal
//
// Write devolve pro UDPConn principal mirando o remoteAddr fixado pelo ICE.
package main

import (
	"errors"
	"net"
	"sync"
	"time"
)

type dtlsPacketConn struct {
	udp        *net.UDPConn
	remoteAddr *net.UDPAddr
	localAddr  net.Addr

	mu       sync.Mutex
	cond     *sync.Cond
	queue    [][]byte
	closed   bool
	deadline time.Time
}

func newDTLSPacketConn(udp *net.UDPConn, remote *net.UDPAddr) *dtlsPacketConn {
	c := &dtlsPacketConn{
		udp:        udp,
		remoteAddr: remote,
		localAddr:  udp.LocalAddr(),
	}
	c.cond = sync.NewCond(&c.mu)
	return c
}

// Push enfileira um datagrama DTLS recebido. Não bloqueia.
func (c *dtlsPacketConn) Push(b []byte) {
	c.mu.Lock()
	if !c.closed {
		dup := make([]byte, len(b))
		copy(dup, b)
		c.queue = append(c.queue, dup)
		c.cond.Broadcast()
	}
	c.mu.Unlock()
}

func (c *dtlsPacketConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for len(c.queue) == 0 {
		if c.closed {
			return 0, net.ErrClosed
		}
		if !c.deadline.IsZero() && !time.Now().Before(c.deadline) {
			return 0, errTimeout{}
		}
		// Sem deadline preciso: usamos um waiter com timeout curto pra reavaliar
		// o deadline. Em pion/dtls handshakes, isso é chamado em loop curto.
		if !c.deadline.IsZero() {
			done := make(chan struct{})
			go func() {
				t := time.NewTimer(time.Until(c.deadline))
				defer t.Stop()
				select {
				case <-t.C:
				case <-done:
				}
				c.mu.Lock()
				c.cond.Broadcast()
				c.mu.Unlock()
			}()
			c.cond.Wait()
			close(done)
		} else {
			c.cond.Wait()
		}
	}
	pkt := c.queue[0]
	c.queue = c.queue[1:]
	n := copy(p, pkt)
	return n, nil
}

func (c *dtlsPacketConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return 0, net.ErrClosed
	}
	return c.udp.WriteToUDP(p, c.remoteAddr)
}

func (c *dtlsPacketConn) Close() error {
	c.mu.Lock()
	c.closed = true
	c.cond.Broadcast()
	c.mu.Unlock()
	return nil
}

func (c *dtlsPacketConn) LocalAddr() net.Addr  { return c.localAddr }
func (c *dtlsPacketConn) RemoteAddr() net.Addr { return c.remoteAddr }

func (c *dtlsPacketConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadline = t
	c.cond.Broadcast()
	c.mu.Unlock()
	return nil
}
func (c *dtlsPacketConn) SetReadDeadline(t time.Time) error  { return c.SetDeadline(t) }
func (c *dtlsPacketConn) SetWriteDeadline(_ time.Time) error { return nil }

type errTimeout struct{}

func (errTimeout) Error() string   { return "i/o timeout" }
func (errTimeout) Timeout() bool   { return true }
func (errTimeout) Temporary() bool { return true }

var _ net.Conn = (*dtlsPacketConn)(nil)

// IsDTLS — RFC 7983 demux: primeiro byte em [20, 63].
func IsDTLS(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	return b[0] >= 20 && b[0] <= 63
}

// (sanity)
var _ = errors.New
