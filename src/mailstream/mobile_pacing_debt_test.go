package mailstream

import (
	"testing"
	"time"
)

func TestRC22RateDropDoesNotRepriceOldQuantum(t *testing.T) {
	at := time.Unix(100, 0)
	var p bulkPacer
	const fast = 8_000_000
	const slow = 16_000
	if !p.readyQuantum(64<<10, fast, at, true) {
		t.Fatal("first flight")
	}
	p.admitted(64 << 10)
	if p.readyQuantum(16<<10, slow, at.Add(time.Millisecond), true) {
		t.Fatal("overlapping bursts")
	}
	if !p.readyQuantum(16<<10, slow, at.Add(100*time.Millisecond), true) {
		t.Fatal("repriced fast burst at slow rate")
	}
	p.admitted(16 << 10)
	if p.readyQuantum(16<<10, slow, at.Add(200*time.Millisecond), true) {
		t.Fatal("slow budget bypassed")
	}
	if !p.readyQuantum(16<<10, slow, at.Add(1200*time.Millisecond), true) {
		t.Fatal("slow budget did not recover")
	}
}
