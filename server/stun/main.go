// stun — implementação RFC 5389 do zero.
// Etapa 1: stub. Implementação real entra na Etapa 3.
package main

import (
	"log"
	"net"
	"os"
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
	defer conn.Close()

	log.Printf("[stun] listening UDP on %s (stub — Etapa 3)", addr)
	buf := make([]byte, 1500)
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			log.Printf("[stun] read: %v", err)
			continue
		}
		log.Printf("[stun] %d bytes from %s (ignored)", n, from)
	}
}
