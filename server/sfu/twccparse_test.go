package main

import (
	"testing"
)

func TestTWCCRoundtrip(t *testing.T) {
	rec := NewTWCCRecorder(0x12345678)
	rec.SetMediaSSRC(0xAABBCCDD)
	base := int64(1_000_000_000)
	rec.Record(100, base)
	rec.Record(101, base+10_000)   // +10ms (small delta)
	rec.Record(102, base+10_500)   // +500us
	// 103 perdido
	rec.Record(104, base+50_000)   // large delta

	fb := rec.Build()
	if fb == nil {
		t.Fatal("nil fb")
	}
	pkts, err := SplitCompound(fb)
	if err != nil || len(pkts) != 1 {
		t.Fatalf("split: %v len=%d", err, len(pkts))
	}
	if !pkts[0].IsTransportCC() {
		t.Fatal("not transport-cc")
	}
	bs, _, arr, err := ParseTWCCFeedback(pkts[0])
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if bs != 100 {
		t.Errorf("baseSeq=%d want 100", bs)
	}
	if len(arr) != 5 {
		t.Fatalf("arrivals=%d want 5", len(arr))
	}
	wantRecv := []bool{true, true, true, false, true}
	for i, a := range arr {
		if a.Received != wantRecv[i] {
			t.Errorf("arr[%d].Received=%v want %v", i, a.Received, wantRecv[i])
		}
		if a.Seq != uint16(100+i) {
			t.Errorf("arr[%d].Seq=%d", i, a.Seq)
		}
	}
}

func TestTWCCRunLengthAllReceived(t *testing.T) {
	rec := NewTWCCRecorder(1)
	rec.SetMediaSSRC(2)
	base := int64(2_000_000_000)
	for i := 0; i < 20; i++ {
		rec.Record(uint16(500+i), base+int64(i)*1000) // 1ms apart
	}
	fb := rec.Build()
	pkts, _ := SplitCompound(fb)
	_, _, arr, err := ParseTWCCFeedback(pkts[0])
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(arr) != 20 {
		t.Fatalf("len=%d", len(arr))
	}
	for i, a := range arr {
		if !a.Received {
			t.Errorf("arr[%d] not received", i)
		}
	}
}
