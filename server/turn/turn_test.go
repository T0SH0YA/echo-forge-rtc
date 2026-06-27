package main

import (
	"net"
	"testing"
	"time"
)

// helper: cria server numa porta efêmera.
func startTestServer(t *testing.T) (*Server, *net.UDPAddr, func()) {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{
		conn:   conn,
		allocs: newAllocManager(net.ParseIP("127.0.0.1"), "127.0.0.1"),
		creds: &credStore{
			realm:      "test",
			staticUser: "u",
			staticPass: "p",
			nonces:     map[string]time.Time{},
		},
	}
	go srv.serve()
	return srv, conn.LocalAddr().(*net.UDPAddr), func() { _ = conn.Close() }
}

func sendRecv(t *testing.T, client *net.UDPConn, srv *net.UDPAddr, payload []byte) []byte {
	t.Helper()
	if _, err := client.WriteToUDP(payload, srv); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 65535)
	n, _, err := client.ReadFromUDP(buf)
	if err != nil {
		t.Fatal(err)
	}
	return buf[:n]
}

func TestAllocateChallengeAndSuccess(t *testing.T) {
	_, srvAddr, stop := startTestServer(t)
	defer stop()

	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// 1) Allocate sem MI → espera 401 com REALM + NONCE.
	tid := [TIDSize]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	req := &Message{Type: msgType(methodAllocate, classRequest), TransactionID: tid}
	req.Add(AttrRequestedTransport, []byte{17, 0, 0, 0})
	raw := req.Encode()
	resp := sendRecv(t, client, srvAddr, raw)
	m, err := Decode(resp)
	if err != nil {
		t.Fatalf("decode challenge: %v", err)
	}
	if classOf(m.Type) != classError {
		t.Fatalf("expected error class, got 0x%x", m.Type)
	}
	ec, _ := m.Get(AttrErrorCode)
	if ec == nil || int(ec[2])*100+int(ec[3]) != 401 {
		t.Fatalf("expected 401, got %v", ec)
	}
	realm, _ := m.Get(AttrRealm)
	nonce, _ := m.Get(AttrNonce)
	if len(realm) == 0 || len(nonce) == 0 {
		t.Fatal("missing realm/nonce in challenge")
	}

	// 2) Allocate com credenciais → espera success com XOR-RELAYED-ADDRESS.
	tid2 := [TIDSize]byte{9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9}
	req2 := &Message{Type: msgType(methodAllocate, classRequest), TransactionID: tid2}
	req2.Add(AttrRequestedTransport, []byte{17, 0, 0, 0})
	req2.Add(AttrUsername, []byte("u"))
	req2.Add(AttrRealm, realm)
	req2.Add(AttrNonce, nonce)
	key := LongTermKey("u", "test", "p")
	raw2 := AppendMessageIntegrity(req2.Encode(), key)
	resp2 := sendRecv(t, client, srvAddr, raw2)
	m2, err := Decode(resp2)
	if err != nil {
		t.Fatalf("decode success: %v", err)
	}
	if classOf(m2.Type) != classSuccess {
		ec2, _ := m2.Get(AttrErrorCode)
		t.Fatalf("expected success, got type=0x%x err=%v", m2.Type, ec2)
	}
	relayedV, ok := m2.Get(AttrXORRelayedAddress)
	if !ok {
		t.Fatal("missing XOR-RELAYED-ADDRESS")
	}
	relayed, err := DecodeXORAddr(relayedV, tid2)
	if err != nil {
		t.Fatal(err)
	}
	if relayed.Port == 0 {
		t.Fatal("relayed port is 0")
	}

	// 3) CreatePermission + Send indication → peer recebe DATA.
	peer, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	peerAddr := peer.LocalAddr().(*net.UDPAddr)

	tid3 := [TIDSize]byte{3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3}
	cp := &Message{Type: msgType(methodCreatePermission, classRequest), TransactionID: tid3}
	cp.Add(AttrXORPeerAddress, EncodeXORAddr(peerAddr, tid3))
	cp.Add(AttrUsername, []byte("u"))
	cp.Add(AttrRealm, realm)
	cp.Add(AttrNonce, nonce)
	cpRaw := AppendMessageIntegrity(cp.Encode(), key)
	cpResp := sendRecv(t, client, srvAddr, cpRaw)
	cpMsg, _ := Decode(cpResp)
	if classOf(cpMsg.Type) != classSuccess {
		t.Fatalf("perm failed: type=0x%x", cpMsg.Type)
	}

	// Send indication.
	tid4 := [TIDSize]byte{4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4}
	si := &Message{Type: msgType(methodSend, classIndication), TransactionID: tid4}
	si.Add(AttrXORPeerAddress, EncodeXORAddr(peerAddr, tid4))
	payload := []byte("hello from client via turn")
	si.Add(AttrData, payload)
	if _, err := client.WriteToUDP(si.Encode(), srvAddr); err != nil {
		t.Fatal(err)
	}

	_ = peer.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	n, gotFrom, err := peer.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("peer read: %v", err)
	}
	if string(buf[:n]) != string(payload) {
		t.Fatalf("payload mismatch: %q", buf[:n])
	}
	if gotFrom.Port != relayed.Port {
		t.Fatalf("expected from relay port %d, got %d", relayed.Port, gotFrom.Port)
	}

	// 4) Peer responde → cliente recebe Data indication.
	reply := []byte("ack from peer")
	if _, err := peer.WriteToUDP(reply, gotFrom); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	cb := make([]byte, 4096)
	cn, _, err := client.ReadFromUDP(cb)
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	dm, err := Decode(cb[:cn])
	if err != nil {
		t.Fatalf("decode data ind: %v", err)
	}
	if methodOf(dm.Type) != methodData || classOf(dm.Type) != classIndication {
		t.Fatalf("expected Data indication, got 0x%x", dm.Type)
	}
	gotData, _ := dm.Get(AttrData)
	if string(gotData) != string(reply) {
		t.Fatalf("data mismatch: %q", gotData)
	}
}

func TestBindingRequest(t *testing.T) {
	_, srvAddr, stop := startTestServer(t)
	defer stop()
	client, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	defer client.Close()
	tid := [TIDSize]byte{7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7}
	req := &Message{Type: msgType(methodBinding, classRequest), TransactionID: tid}
	resp := sendRecv(t, client, srvAddr, req.Encode())
	m, err := Decode(resp)
	if err != nil {
		t.Fatal(err)
	}
	if classOf(m.Type) != classSuccess || methodOf(m.Type) != methodBinding {
		t.Fatalf("bad type 0x%x", m.Type)
	}
	if _, ok := m.Get(AttrXORMappedAddress); !ok {
		t.Fatal("missing xor-mapped")
	}
}

func TestEphemeralCreds(t *testing.T) {
	user, pass := GenerateEphemeral("topsecret", "alice", time.Hour)
	store := &credStore{realm: "r", authSecret: "topsecret", nonces: map[string]time.Time{}}
	key := store.keyFor(user)
	if key == nil {
		t.Fatal("ephemeral key nil")
	}
	expected := LongTermKey(user, "r", pass)
	if string(key) != string(expected) {
		t.Fatal("ephemeral key mismatch")
	}
	// Expirado deve falhar.
	expUser, _ := GenerateEphemeral("topsecret", "bob", -time.Hour)
	if store.keyFor(expUser) != nil {
		t.Fatal("expired user accepted")
	}
}
