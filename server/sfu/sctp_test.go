package main

import (
	"bytes"
	"testing"
)

func TestParseDCEPOpen(t *testing.T) {
	// label="chat", protocol="" → labelLen=4 protoLen=0
	payload := append([]byte{
		DCEPMsgOpen,    // type
		0x00,           // ChannelType = reliable
		0x01, 0x00,     // Priority = 256
		0x00, 0x00, 0x00, 0x00, // ReliabilityParameter
		0x00, 0x04,     // LabelLen = 4
		0x00, 0x00,     // ProtoLen = 0
	}, []byte("chat")...)
	open, err := ParseDCEPOpen(payload)
	if err != nil {
		t.Fatal(err)
	}
	if open.Label != "chat" {
		t.Errorf("label %q", open.Label)
	}
	if open.Priority != 256 {
		t.Errorf("priority %d", open.Priority)
	}
	if open.Protocol != "" {
		t.Errorf("proto %q", open.Protocol)
	}
}

func TestParseDCEPOpenRejectsNonOpen(t *testing.T) {
	if _, err := ParseDCEPOpen([]byte{DCEPMsgAck}); err == nil {
		t.Error("ack should not parse as open")
	}
	if _, err := ParseDCEPOpen(nil); err == nil {
		t.Error("empty should not parse")
	}
}

func TestParseDCEPOpenTruncated(t *testing.T) {
	// LabelLen=10 mas só 3 bytes restantes
	payload := []byte{DCEPMsgOpen, 0, 0, 0, 0, 0, 0, 0, 0, 10, 0, 0, 'a', 'b', 'c'}
	if _, err := ParseDCEPOpen(payload); err == nil {
		t.Error("truncated should fail")
	}
}

func TestBuildDCEPOpenRoundtrip(t *testing.T) {
	enc := buildDCEPOpen("file-transfer")
	open, err := ParseDCEPOpen(enc)
	if err != nil {
		t.Fatal(err)
	}
	if open.Label != "file-transfer" {
		t.Errorf("label %q", open.Label)
	}
}

func TestBuildDCEPAck(t *testing.T) {
	if !bytes.Equal(BuildDCEPAck(), []byte{0x02}) {
		t.Error("ack should be single byte 0x02")
	}
}

func TestDCEPProtocolField(t *testing.T) {
	// label="data", protocol="json"
	payload := append([]byte{
		DCEPMsgOpen, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x04, 0x00, 0x04,
	}, []byte("datajson")...)
	open, err := ParseDCEPOpen(payload)
	if err != nil {
		t.Fatal(err)
	}
	if open.Label != "data" || open.Protocol != "json" {
		t.Errorf("got label=%q proto=%q", open.Label, open.Protocol)
	}
}
