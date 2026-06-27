package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const sampleOffer = `v=0
o=- 4611732 2 IN IP4 127.0.0.1
s=-
t=0 0
a=group:BUNDLE 0
a=msid-semantic: WMS
m=audio 9 UDP/TLS/RTP/SAVPF 111
c=IN IP4 0.0.0.0
a=rtcp-mux
a=ice-ufrag:abcd
a=ice-pwd:abcdefghijklmnopqrstuv
a=fingerprint:sha-256 11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00
a=setup:actpass
a=mid:0
a=sendrecv
a=rtpmap:111 opus/48000/2
`

func TestSDPParseAndAnswer(t *testing.T) {
	off, err := ParseOffer(sampleOffer)
	if err != nil {
		t.Fatal(err)
	}
	if off.IceUfrag != "abcd" || off.IcePwd != "abcdefghijklmnopqrstuv" {
		t.Fatalf("creds: %+v", off)
	}
	if len(off.Media) != 1 || off.Media[0].Kind != "audio" || off.Media[0].Mid != "0" {
		t.Fatalf("media: %+v", off.Media)
	}
	ans := BuildAnswer(off, AnswerParams{
		IceUfrag: "xyz1", IcePwd: "passpasspasspasspasspass",
		Fingerprint: "sha-256 AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99", HostIP: "1.2.3.4", HostPort: 7000,
	})
	for _, want := range []string{
		"a=ice-lite",
		"a=ice-ufrag:xyz1",
		"a=setup:active", // offer veio actpass; respondemos passive… espera, actpass não cai em passive aqui
		"a=candidate:1 1 UDP",
		"a=end-of-candidates",
		"a=rtcp-mux",
	} {
		if !strings.Contains(ans, want) {
			// "setup:active" só ocorre quando offer disse passive; aqui actpass→passive.
			if want == "a=setup:active" {
				if !strings.Contains(ans, "a=setup:passive") {
					t.Errorf("missing setup line in answer:\n%s", ans)
				}
				continue
			}
			t.Errorf("answer missing %q:\n%s", want, ans)
		}
	}
}

func TestHTTPOfferAnswerAndICEBinding(t *testing.T) {
	// Sobe UDP em porta efêmera local.
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

	// POST /sessions
	ts := httptest.NewServer(http.HandlerFunc(srv.handleNewSession))
	defer ts.Close()

	body, _ := json.Marshal(offerReq{Type: "offer", SDP: sampleOffer})
	resp, err := http.Post(ts.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var ans answerResp
	if err := json.NewDecoder(resp.Body).Decode(&ans); err != nil {
		t.Fatal(err)
	}
	if ans.SessionID == "" || !strings.Contains(ans.SDP, "a=ice-lite") {
		t.Fatalf("bad answer: %+v", ans)
	}
	sess := srv.sessions.ByID(ans.SessionID)
	if sess == nil {
		t.Fatal("session missing")
	}

	// Manda STUN Binding com USERNAME "localUfrag:remoteUfrag" + MI assinado com sess.LocalPwd.
	client, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	defer client.Close()

	tid := [TIDSize]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	req := &Message{Type: msgType(methodBinding, classRequest), TransactionID: tid}
	req.Add(AttrUsername, []byte(sess.LocalUfrag+":"+sess.RemoteUfrag))
	req.Add(AttrUseCandidate, nil)
	raw := AppendMessageIntegrity(req.Encode(), []byte(sess.LocalPwd))
	raw = AppendFingerprint(raw)

	if _, err := client.WriteToUDP(raw, udp.LocalAddr().(*net.UDPAddr)); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1500)
	n, _, err := client.ReadFromUDP(buf)
	if err != nil {
		t.Fatal(err)
	}
	m, err := Decode(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	if classOf(m.Type) != classSuccess {
		ec, _ := m.Get(AttrErrorCode)
		t.Fatalf("expected success, got 0x%x err=%v", m.Type, ec)
	}
	if !VerifyMessageIntegrity(buf[:n], []byte(sess.LocalPwd)) {
		t.Fatal("response MI invalid")
	}

	// Aguarda goroutine processar USE-CANDIDATE.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if sess.State() == ICEConnected {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if sess.State() != ICEConnected {
		t.Fatalf("expected ICEConnected, got %s", sess.State())
	}
	if sess.RemoteAddr() == "" {
		t.Fatal("remote addr not set")
	}
}

func TestSTUNBindingBadMI(t *testing.T) {
	udp, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	defer udp.Close()
	srv := &Server{udp: udp, publicIP: "127.0.0.1", udpPort: udp.LocalAddr().(*net.UDPAddr).Port, sessions: newSessionStore()}
	go srv.udpLoop()
	sess := &Session{ID: "s1", LocalUfrag: "luf", LocalPwd: "lpw" + strings.Repeat("x", 22), RemoteUfrag: "ruf", RemotePwd: "rpw"}
	srv.sessions.Add(sess)

	client, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	defer client.Close()
	tid := [TIDSize]byte{9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9}
	req := &Message{Type: msgType(methodBinding, classRequest), TransactionID: tid}
	req.Add(AttrUsername, []byte("luf:ruf"))
	raw := AppendMessageIntegrity(req.Encode(), []byte("wrong-key-wrong-key-wrong"))
	raw = AppendFingerprint(raw)
	if _, err := client.WriteToUDP(raw, udp.LocalAddr().(*net.UDPAddr)); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1500)
	n, _, err := client.ReadFromUDP(buf)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := Decode(buf[:n])
	if classOf(m.Type) != classError {
		t.Fatalf("expected error, got 0x%x", m.Type)
	}
}
