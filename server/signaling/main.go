// signaling — WebSocket de sinalização. Sala em memória, broadcast SDP/ICE.
// Stateless por mensagem: não inspeciona SDP, só repassa byte a byte.
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/webrtc-own/common"
)

const (
	writeTimeout   = 10 * time.Second
	pongTimeout    = 30 * time.Second
	pingInterval   = 20 * time.Second
	helloTimeout   = 5 * time.Second
	maxMessageSize = 256 * 1024
	maxRoomSize    = 50
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(_ *http.Request) bool { return true }, // CORS amplo; aperte em prod via token
}

// ---------- modelo de sala ----------

type peer struct {
	id   string
	role string
	send chan []byte
	conn *websocket.Conn
	room *room
}

type room struct {
	id    string
	mu    sync.RWMutex
	peers map[string]*peer
}

func (r *room) add(p *peer) []common.PeerInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing := make([]common.PeerInfo, 0, len(r.peers))
	for _, other := range r.peers {
		existing = append(existing, common.PeerInfo{ID: other.id, Role: other.role})
	}
	r.peers[p.id] = p
	return existing
}

func (r *room) remove(id string) {
	r.mu.Lock()
	delete(r.peers, id)
	r.mu.Unlock()
}

func (r *room) get(id string) *peer {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.peers[id]
}

func (r *room) broadcast(except string, payload []byte) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for id, p := range r.peers {
		if id == except {
			continue
		}
		select {
		case p.send <- payload:
		default:
			// canal cheio: descarta esse peer
			log.Printf("[signaling] peer %s send buffer cheio; descartando", id)
		}
	}
}

func (r *room) size() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.peers)
}

// ---------- hub global de salas ----------

type hub struct {
	mu    sync.Mutex
	rooms map[string]*room
}

func newHub() *hub {
	return &hub{rooms: make(map[string]*room)}
}

func (h *hub) getOrCreate(id string) *room {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r, ok := h.rooms[id]; ok {
		return r
	}
	r := &room{id: id, peers: make(map[string]*peer)}
	h.rooms[id] = r
	return r
}

func (h *hub) gc(r *room) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r.size() == 0 {
		delete(h.rooms, r.id)
	}
}

// ---------- handlers ----------

func main() {
	addr := os.Getenv("SIGNALING_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	h := newHub()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/v1/rooms/", func(w http.ResponseWriter, r *http.Request) {
		serveRoom(h, w, r)
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("[signaling] listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[signaling] %v", err)
		}
	}()

	<-ctx.Done()
	log.Printf("[signaling] shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

func serveRoom(h *hub, w http.ResponseWriter, r *http.Request) {
	// Path: /v1/rooms/<roomId>
	roomID := r.URL.Path[len("/v1/rooms/"):]
	if roomID == "" {
		http.Error(w, "missing room id", http.StatusBadRequest)
		return
	}

	// TODO Etapa 4+: validar JWT (claim `room` deve bater com roomID).
	token := r.URL.Query().Get("token")
	_ = token

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[signaling] upgrade: %v", err)
		return
	}
	conn.SetReadLimit(maxMessageSize)

	rm := h.getOrCreate(roomID)
	if rm.size() >= maxRoomSize {
		_ = sendError(conn, "ROOM_FULL", "room at capacity")
		_ = conn.Close()
		return
	}

	p := &peer{
		id:   "p_" + uuid.NewString()[:8],
		send: make(chan []byte, 64),
		conn: conn,
		room: rm,
	}

	// Espera hello primeiro
	_ = conn.SetReadDeadline(time.Now().Add(helloTimeout))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		_ = conn.Close()
		return
	}
	var env common.Envelope
	if err := json.Unmarshal(raw, &env); err != nil || env.T != "hello" {
		_ = sendError(conn, "INVALID", "expected hello")
		_ = conn.Close()
		return
	}
	_ = conn.SetReadDeadline(time.Now().Add(pongTimeout))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(pongTimeout))
	})

	peers := rm.add(p)

	// welcome
	welcome := common.Envelope{T: "welcome", Data: common.Welcome{
		PeerID: p.id,
		Room:   rm.id,
		Peers:  peers,
		// Etapa 3/4: STUN/TURN próprios. Por enquanto sem ICE servers (LAN/Trickle).
		IceServers: []common.IceServer{},
	}}
	if err := writeJSON(conn, welcome); err != nil {
		rm.remove(p.id)
		_ = conn.Close()
		return
	}

	// avisa outros
	joined, _ := json.Marshal(common.Envelope{T: "peer-join", Data: map[string]any{
		"peer": common.PeerInfo{ID: p.id, Role: p.role},
	}})
	rm.broadcast(p.id, joined)

	go writePump(p)
	readPump(h, p)
}

func readPump(h *hub, p *peer) {
	defer func() {
		p.room.remove(p.id)
		left, _ := json.Marshal(common.Envelope{T: "peer-leave", Data: map[string]any{"peerId": p.id}})
		p.room.broadcast(p.id, left)
		close(p.send)
		_ = p.conn.Close()
		h.gc(p.room)
	}()

	for {
		_, raw, err := p.conn.ReadMessage()
		if err != nil {
			return
		}
		var env struct {
			T    string          `json:"t"`
			ID   string          `json:"id,omitempty"`
			Data json.RawMessage `json:"data,omitempty"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			_ = sendErrorEnv(p, "INVALID", "bad json")
			continue
		}

		switch env.T {
		case "offer", "answer", "ice":
			// extrai `to`, injeta `from`, repassa byte a byte
			var addressed map[string]any
			if err := json.Unmarshal(env.Data, &addressed); err != nil {
				_ = sendErrorEnv(p, "INVALID", "bad data")
				continue
			}
			toRaw, _ := addressed["to"].(string)
			if toRaw == "" {
				_ = sendErrorEnv(p, "INVALID", "missing to")
				continue
			}
			delete(addressed, "to")
			addressed["from"] = p.id
			out, _ := json.Marshal(common.Envelope{T: env.T, ID: env.ID, Data: addressed})

			target := p.room.get(toRaw)
			if target == nil {
				_ = sendErrorEnv(p, "PEER_NOT_FOUND", toRaw)
				continue
			}
			select {
			case target.send <- out:
			default:
				log.Printf("[signaling] drop to %s (buffer cheio)", toRaw)
			}

		case "leave":
			return

		default:
			_ = sendErrorEnv(p, "INVALID", "unknown type "+env.T)
		}
	}
}

func writePump(p *peer) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case msg, ok := <-p.send:
			_ = p.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if !ok {
				_ = p.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := p.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = p.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := p.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// ---------- utils ----------

func writeJSON(c *websocket.Conn, v any) error {
	_ = c.SetWriteDeadline(time.Now().Add(writeTimeout))
	return c.WriteJSON(v)
}

func sendError(c *websocket.Conn, code, msg string) error {
	return writeJSON(c, common.Envelope{T: "error", Data: common.ErrorMessage{Code: code, Message: msg}})
}

func sendErrorEnv(p *peer, code, msg string) error {
	out, _ := json.Marshal(common.Envelope{T: "error", Data: common.ErrorMessage{Code: code, Message: msg}})
	select {
	case p.send <- out:
		return nil
	default:
		return nil
	}
}
