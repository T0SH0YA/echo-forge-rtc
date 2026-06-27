// turn — implementação RFC 5766/8656 do zero.
// Etapa 1: stub. Implementação real entra na Etapa 4.
package main

import (
	"log"
	"os"
)

func main() {
	addr := os.Getenv("TURN_ADDR")
	if addr == "" {
		addr = ":3478"
	}
	log.Printf("[turn] stub — Etapa 4 (would bind %s)", addr)
	select {} // bloqueia
}
