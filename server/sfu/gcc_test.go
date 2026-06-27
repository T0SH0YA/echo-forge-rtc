package main

import "testing"

func TestDownstreamBWEOveruseDecreases(t *testing.T) {
	bwe := NewDownstreamBWE()
	start := bwe.Estimate()
	// 20 pacotes enviados a cada 10ms
	arrivals := make([]TWCCArrival, 20)
	sent := int64(0)
	for i := 0; i < 20; i++ {
		bwe.RecordSent(uint16(i), sent, 1200*8)
		sent += 10_000
	}
	// chegadas com delay crescente (queueing): cada pacote chega 2ms a mais tarde
	arr := int64(100_000)
	for i := 0; i < 20; i++ {
		arrivals[i] = TWCCArrival{Seq: uint16(i), Received: true, ArrivalUS: arr}
		arr += 12_000 // +2ms a mais que sent gap → overuse
	}
	bwe.OnFeedback(arrivals)
	if bwe.Estimate() >= start {
		t.Errorf("expected decrease on overuse, got %d (start=%d)", bwe.Estimate(), start)
	}
}

func TestDownstreamBWECleanIncreases(t *testing.T) {
	bwe := NewDownstreamBWE()
	start := bwe.Estimate()
	sent := int64(0)
	arrivals := make([]TWCCArrival, 30)
	arr := int64(100_000)
	for i := 0; i < 30; i++ {
		bwe.RecordSent(uint16(i), sent, 1200*8)
		arrivals[i] = TWCCArrival{Seq: uint16(i), Received: true, ArrivalUS: arr}
		sent += 10_000
		arr += 10_000 // mesmo gap → sem overuse
	}
	bwe.OnFeedback(arrivals)
	if bwe.Estimate() <= start {
		t.Errorf("expected increase on clean, got %d (start=%d)", bwe.Estimate(), start)
	}
}

func TestDownstreamBWELossDecreases(t *testing.T) {
	bwe := NewDownstreamBWE()
	start := bwe.Estimate()
	sent := int64(0)
	arrivals := make([]TWCCArrival, 20)
	arr := int64(100_000)
	for i := 0; i < 20; i++ {
		bwe.RecordSent(uint16(i), sent, 1000*8)
		arrivals[i] = TWCCArrival{Seq: uint16(i), Received: i%4 != 0, ArrivalUS: arr}
		sent += 10_000
		if arrivals[i].Received {
			arr += 10_000
		}
	}
	bwe.OnFeedback(arrivals)
	if bwe.Estimate() >= start {
		t.Errorf("expected decrease on loss, got %d", bwe.Estimate())
	}
}

func TestPickLayer(t *testing.T) {
	layers := []string{"q", "h", "f"}
	if r := PickLayer(150_000, layers); r != "q" {
		t.Errorf("low budget → %s want q", r)
	}
	if r := PickLayer(800_000, layers); r != "h" {
		t.Errorf("mid budget → %s want h", r)
	}
	if r := PickLayer(3_000_000, layers); r != "f" {
		t.Errorf("high budget → %s want f", r)
	}
}

func TestRewriteOneByteExt(t *testing.T) {
	// id=5, length=2, value=[0xAA,0xBB]
	ext := []byte{0x51, 0xAA, 0xBB}
	if !RewriteOneByteExtValue(0xBEDE, ext, 5, []byte{0xCC, 0xDD}) {
		t.Fatal("rewrite failed")
	}
	if ext[1] != 0xCC || ext[2] != 0xDD {
		t.Errorf("ext not rewritten: %x", ext)
	}
}
