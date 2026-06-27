// signaling — servidor WebSocket de sinalização.
// Etapa 1: stub. Implementação real (rooms, broadcast SDP/ICE) entra na Etapa 2.
package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	addr := os.Getenv("SIGNALING_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	log.Printf("[signaling] listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("[signaling] %v", err)
	}
}
