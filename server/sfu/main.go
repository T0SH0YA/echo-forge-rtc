// sfu — Selective Forwarding Unit própria.
// Etapa 1: stub. ICE/DTLS/SRTP entram nas Etapas 5–9.
package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	addr := os.Getenv("SFU_ADDR")
	if addr == "" {
		addr = ":8081"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	log.Printf("[sfu] listening on %s (stub — Etapas 5-9)", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("[sfu] %v", err)
	}
}
