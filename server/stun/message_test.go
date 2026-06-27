package main

import (
	"bytes"
	"net"
	"testing"
)

func TestRoundTripBindingRequest(t *testing.T) {
	m := &Message{Type: BindingRequest}
	copy(m.TransactionID[:], []byte("123456789012"))
	m.AddAttribute(AttrSoftware, []byte("webrtc-own"))
	raw := m.Encode()
	if len(raw)%4 != 0 {
		t.Fatalf("len not multiple of 4: %d", len(raw))
	}
	d, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if d.Type != BindingRequest {
		t.Fatalf("type=%x", d.Type)
	}
	if !bytes.Equal(d.TransactionID[:], m.TransactionID[:]) {
		t.Fatal("tid mismatch")
	}
	if v, ok := d.Get(AttrSoftware); !ok || string(v) != "webrtc-own" {
		t.Fatalf("software=%q ok=%v", v, ok)
	}
}

func TestXORMappedAddressIPv4(t *testing.T) {
	var tid [TIDSize]byte
	copy(tid[:], []byte("abcdefghijkl"))
	addr := &net.UDPAddr{IP: net.ParseIP("203.0.113.7"), Port: 54321}
	v := EncodeXORMappedAddress(addr, tid)
	got, err := DecodeXORMappedAddress(v, tid)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IP.Equal(addr.IP) || got.Port != addr.Port {
		t.Fatalf("got %v want %v", got, addr)
	}
}

func TestXORMappedAddressIPv6(t *testing.T) {
	var tid [TIDSize]byte
	copy(tid[:], []byte("abcdefghijkl"))
	addr := &net.UDPAddr{IP: net.ParseIP("2001:db8::1"), Port: 9000}
	v := EncodeXORMappedAddress(addr, tid)
	got, err := DecodeXORMappedAddress(v, tid)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IP.Equal(addr.IP) || got.Port != addr.Port {
		t.Fatalf("got %v want %v", got, addr)
	}
}

func TestFingerprintRoundTrip(t *testing.T) {
	m := &Message{Type: BindingRequest}
	copy(m.TransactionID[:], []byte("123456789012"))
	raw := m.Encode()
	raw = AppendFingerprint(raw)
	if !VerifyFingerprint(raw) {
		t.Fatal("fingerprint did not verify")
	}
	// Mutilate one byte → must fail.
	raw[8] ^= 0xFF
	if VerifyFingerprint(raw) {
		t.Fatal("fingerprint verified on tampered message")
	}
}

func TestMessageIntegrityRoundTrip(t *testing.T) {
	m := &Message{Type: BindingRequest}
	copy(m.TransactionID[:], []byte("123456789012"))
	raw := m.Encode()
	key := []byte("secret-key")
	raw = AppendMessageIntegrity(raw, key)
	if !VerifyMessageIntegrity(raw, key) {
		t.Fatal("MI did not verify")
	}
	if VerifyMessageIntegrity(raw, []byte("wrong")) {
		t.Fatal("MI verified with wrong key")
	}
}

func TestMessageIntegrityThenFingerprint(t *testing.T) {
	m := &Message{Type: BindingRequest}
	copy(m.TransactionID[:], []byte("123456789012"))
	raw := m.Encode()
	key := []byte("k")
	raw = AppendMessageIntegrity(raw, key)
	raw = AppendFingerprint(raw)
	if !VerifyFingerprint(raw) {
		t.Fatal("fp fail")
	}
	if !VerifyMessageIntegrity(raw, key) {
		t.Fatal("mi fail when followed by fp")
	}
}

func TestRejectBadCookie(t *testing.T) {
	m := &Message{Type: BindingRequest}
	raw := m.Encode()
	raw[4] = 0xFF
	if _, err := Decode(raw); err == nil {
		t.Fatal("expected error")
	}
}
