// stun — servidor STUN próprio (RFC 5389).
//
// Funcionalidade: responde Binding Requests com XOR-MAPPED-ADDRESS contendo
// o IP:porta de onde o pacote chegou (reflexive transport address). Anexa
// SOFTWARE e FINGERPRINT em toda resposta. Suporta IPv4 e IPv6.
package main

import (
	"context"
	"encoding/binary"
	"log"
	"net"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
)

const softwareName = "webrtc-own-stun/0.1"

var (
	pktIn  atomic.Uint64
	pktOut atomic.Uint64
	pktErr atomic.Uint64
)

func main() {
	addr := os.Getenv("STUN_ADDR")
	if addr == "" {
		addr = ":3478"
	}

	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		log.Fatalf("[stun] resolve: %v", err)
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		log.Fatalf("[stun] listen: %v", err)
	}
	log.Printf("[stun] listening UDP on %s (RFC 5389)", conn.LocalAddr())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go stats(ctx)
	go shutdownOnSignal(cancel, conn)

	serve(conn)
}

func serve(conn *net.UDPConn) {
	buf := make([]byte, 1500)
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			// fechamento explícito sai do loop
			if ne, ok := err.(net.Error); ok && !ne.Timeout() {
				log.Printf("[stun] read: %v", err)
			}
			return
		}
		pktIn.Add(1)
		resp, ok := handle(buf[:n], from)
		if !ok {
			pktErr.Add(1)
			continue
		}
		if _, err := conn.WriteToUDP(resp, from); err != nil {
			pktErr.Add(1)
			log.Printf("[stun] write %s: %v", from, err)
			continue
		}
		pktOut.Add(1)
	}
}

// handle parseia e gera resposta. Retorna (resp, true) ou (nil, false).
func handle(raw []byte, from *net.UDPAddr) ([]byte, bool) {
	msg, err := Decode(raw)
	if err != nil {
		// silencioso pra não virar amplificação de log
		return nil, false
	}
	// Etapa 3: só Binding Request. Indications são descartadas.
	if msg.Type != BindingRequest {
		return nil, false
	}

	resp := &Message{Type: BindingSuccess, TransactionID: msg.TransactionID}
	resp.AddAttribute(AttrXORMappedAddress, EncodeXORMappedAddress(from, msg.TransactionID))
	resp.AddAttribute(AttrSoftware, []byte(softwareName))
	out := resp.Encode()
	out = AppendFingerprint(out)
	return out, true
}

// stats imprime contadores periodicamente.
func stats(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	var lastIn uint64
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			in := pktIn.Load()
			if in == lastIn {
				continue
			}
			lastIn = in
			log.Printf("[stun] in=%d out=%d err=%d", in, pktOut.Load(), pktErr.Load())
		}
	}
}

func shutdownOnSignal(cancel context.CancelFunc, conn *net.UDPConn) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	log.Printf("[stun] shutting down")
	cancel()
	_ = conn.Close()
}

// debug helper (não usado em runtime, mas útil pra inspecionar)
func tidString(b []byte) string {
	if len(b) < 12 {
		return "?"
	}
	return string([]byte{
		hex(b[0] >> 4), hex(b[0] & 0xF),
		hex(b[1] >> 4), hex(b[1] & 0xF),
		hex(b[2] >> 4), hex(b[2] & 0xF),
		hex(b[3] >> 4), hex(b[3] & 0xF),
	})
}
func hex(n byte) byte {
	if n < 10 {
		return '0' + n
	}
	return 'a' + n - 10
}

var _ = binary.BigEndian // silenciar import se não usado em refactors futuros
