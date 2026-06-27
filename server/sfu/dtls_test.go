package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"crypto/tls"

	dtls "github.com/pion/dtls/v2"
)

func makeClientCert(t *testing.T) (*tls.Certificate, []byte) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "client"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv, Leaf: leaf}, der
}

func TestFingerprintFormat(t *testing.T) {
	der := []byte{1, 2, 3, 4, 5}
	fp := FingerprintSHA256(der)
	if !strings.HasPrefix(fp, "sha-256 ") {
		t.Fatal("missing prefix")
	}
	parts := strings.Split(strings.TrimPrefix(fp, "sha-256 "), ":")
	if len(parts) != 32 {
		t.Fatalf("want 32 octets, got %d", len(parts))
	}
	for _, p := range parts {
		if len(p) != 2 {
			t.Fatalf("bad octet %q", p)
		}
		if _, err := hex.DecodeString(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := matchFingerprint(fp, der); err != nil {
		t.Fatal(err)
	}
	if err := matchFingerprint(fp, []byte("other")); err == nil {
		t.Fatal("expected mismatch")
	}
}

// TestFullDTLSHandshake faz o caminho completo: HTTP /sessions → STUN binding
// com USE-CANDIDATE → handshake DTLS real (pion como cliente) → checa que o
// servidor extraiu SRTP keying material.
func TestFullDTLSHandshake(t *testing.T) {
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	srv := &Server{
		udp:      udp,
		publicIP: "127.0.0.1",
		udpPort:  udp.LocalAddr().(*net.UDPAddr).Port,
		sessions: newSessionStore(),
	}
	go srv.udpLoop()

	// Cert do cliente — vamos colocar o fingerprint dele no offer.
	clientCert, clientDER := makeClientCert(t)
	clientFP := FingerprintSHA256(clientDER)

	offer := strings.ReplaceAll(sampleOffer,
		"sha-256 11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00",
		clientFP)

	ts := httptest.NewServer(http.HandlerFunc(srv.handleNewSession))
	defer ts.Close()
	body, _ := json.Marshal(offerReq{Type: "offer", SDP: offer})
	resp, err := http.Post(ts.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var ans answerResp
	_ = json.NewDecoder(resp.Body).Decode(&ans)
	sess := srv.sessions.ByID(ans.SessionID)
	if sess == nil {
		t.Fatal("session missing")
	}
	if !strings.Contains(ans.SDP, sess.LocalFingerprint) {
		t.Fatal("answer missing local fingerprint")
	}

	// Socket UDP que o "cliente" usa pra falar STUN + DTLS.
	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	serverAddr := udp.LocalAddr().(*net.UDPAddr)

	// 1) STUN Binding com USE-CANDIDATE pra promover sessão a ICEConnected.
	tid := [TIDSize]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	req := &Message{Type: msgType(methodBinding, classRequest), TransactionID: tid}
	req.Add(AttrUsername, []byte(sess.LocalUfrag+":"+sess.RemoteUfrag))
	req.Add(AttrUseCandidate, nil)
	raw := AppendMessageIntegrity(req.Encode(), []byte(sess.LocalPwd))
	raw = AppendFingerprint(raw)
	if _, err := client.WriteToUDP(raw, serverAddr); err != nil {
		t.Fatal(err)
	}
	// Drena a resposta STUN (precisa ler senão acumula).
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 1500)
	if _, _, err := client.ReadFromUDP(buf); err != nil {
		t.Fatal(err)
	}

	// Aguarda ICE connected.
	waitFor(t, time.Second, func() bool { return sess.State() == ICEConnected })

	// Reseta deadline pra DTLS gerenciar.
	_ = client.SetReadDeadline(time.Time{})

	// 2) Dispara DTLS client (pion) usando o MESMO socket UDP do cliente.
	clientConn := &udpConnAdapter{pc: client, remote: serverAddr}

	cfg := &dtls.Config{
		Certificates:           []tls.Certificate{*clientCert},
		SRTPProtectionProfiles: srtpProfiles,
		InsecureSkipVerify:     true,
		ExtendedMasterSecret:   dtls.RequireExtendedMasterSecret,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dconn, err := dtls.ClientWithContext(ctx, clientConn, cfg)
	if err != nil {
		t.Fatalf("dtls client handshake: %v", err)
	}
	defer dconn.Close()

	// 3) Confirma que o servidor terminou o handshake e extraiu chaves.
	waitFor(t, 3*time.Second, func() bool {
		sess.mu.Lock()
		defer sess.mu.Unlock()
		return sess.dtlsState == DTLSEstablished && sess.srtpKeys != nil
	})
	sess.mu.Lock()
	keys := sess.srtpKeys
	state := sess.dtlsState
	sess.mu.Unlock()
	if state != DTLSEstablished {
		t.Fatalf("server dtls state = %v", state)
	}
	if keys == nil {
		t.Fatal("no SRTP keys extracted")
	}
	if len(keys.ClientKey) == 0 || len(keys.ServerKey) == 0 ||
		len(keys.ClientSalt) == 0 || len(keys.ServerSalt) == 0 {
		t.Fatalf("keys malformed: %+v", keys)
	}
	t.Logf("DTLS established, SRTP profile=0x%04x keyLen=%d saltLen=%d",
		uint16(keys.Profile), len(keys.ClientKey), len(keys.ClientSalt))
}

// udpConnAdapter expõe um *net.UDPConn não-conectado como net.Conn fixado num
// remoto, pra alimentar dtls.Client.
type udpConnAdapter struct {
	pc     *net.UDPConn
	remote *net.UDPAddr
}

func (a *udpConnAdapter) Read(b []byte) (int, error) {
	n, _, err := a.pc.ReadFromUDP(b)
	return n, err
}
func (a *udpConnAdapter) Write(b []byte) (int, error)        { return a.pc.WriteToUDP(b, a.remote) }
func (a *udpConnAdapter) Close() error                       { return nil } // dono é o teste
func (a *udpConnAdapter) LocalAddr() net.Addr                { return a.pc.LocalAddr() }
func (a *udpConnAdapter) RemoteAddr() net.Addr               { return a.remote }
func (a *udpConnAdapter) SetDeadline(t time.Time) error      { return a.pc.SetDeadline(t) }
func (a *udpConnAdapter) SetReadDeadline(t time.Time) error  { return a.pc.SetReadDeadline(t) }
func (a *udpConnAdapter) SetWriteDeadline(t time.Time) error { return a.pc.SetWriteDeadline(t) }

func waitFor(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", d)
}
