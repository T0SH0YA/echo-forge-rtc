// Long-term credentials + ephemeral HMAC creds (formato coturn).
//
// Modo estático: TURN_STATIC_USER + TURN_STATIC_PASS no env. Senha em claro.
// Modo efêmero: TURN_AUTH_SECRET no env. Username = "expiry:userid",
// password = base64(HMAC-SHA1(secret, username)). Sinalização gera essas
// credenciais on-demand pra cada peer.
package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultRealm = "webrtc-own"

type credStore struct {
	realm       string
	staticUser  string
	staticPass  string
	authSecret  string
	mu          sync.Mutex
	nonces      map[string]time.Time
}

func newCredStore() *credStore {
	realm := os.Getenv("TURN_REALM")
	if realm == "" {
		realm = defaultRealm
	}
	return &credStore{
		realm:      realm,
		staticUser: os.Getenv("TURN_STATIC_USER"),
		staticPass: os.Getenv("TURN_STATIC_PASS"),
		authSecret: os.Getenv("TURN_AUTH_SECRET"),
		nonces:     make(map[string]time.Time),
	}
}

// newNonce gera um nonce hex de 16 bytes e o registra com TTL 10min.
func (c *credStore) newNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	n := hex.EncodeToString(b)
	c.mu.Lock()
	c.nonces[n] = time.Now().Add(10 * time.Minute)
	c.mu.Unlock()
	return n
}

func (c *credStore) validNonce(n string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	exp, ok := c.nonces[n]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(c.nonces, n)
		return false
	}
	return true
}

// keyFor devolve a chave long-term pro username dado, ou nil se rejeitado.
func (c *credStore) keyFor(user string) []byte {
	// Tenta efêmero primeiro: username precisa começar com "<unixts>:".
	if c.authSecret != "" {
		if colon := strings.IndexByte(user, ':'); colon > 0 {
			tsStr := user[:colon]
			if ts, err := strconv.ParseInt(tsStr, 10, 64); err == nil {
				if time.Now().Unix() <= ts {
					mac := hmac.New(sha1.New, []byte(c.authSecret))
					mac.Write([]byte(user))
					pass := base64.StdEncoding.EncodeToString(mac.Sum(nil))
					return LongTermKey(user, c.realm, pass)
				}
			}
		}
	}
	// Fallback estático.
	if c.staticUser != "" && user == c.staticUser {
		return LongTermKey(c.staticUser, c.realm, c.staticPass)
	}
	return nil
}

// GenerateEphemeral devolve (username, password) válidos por ttl segundos,
// usados pela sinalização pra entregar pro SDK. Não usado no servidor TURN
// em si, mas exportado pra reutilização.
func GenerateEphemeral(secret, userID string, ttl time.Duration) (string, string) {
	exp := time.Now().Add(ttl).Unix()
	user := strconv.FormatInt(exp, 10) + ":" + userID
	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write([]byte(user))
	pass := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return user, pass
}
