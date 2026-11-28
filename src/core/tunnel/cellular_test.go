package tunnel

import (
	"itom.local/mailstream"
	"testing"
	"time"
)

func TestCellularQualityEvidence(t *testing.T) {
	var h bearerHistory
	h.observe([]float64{30, 35, 40}, 35, mailstream.Snapshot{}, 1000000)
	e := h.observe([]float64{30, 40, 400}, 40, mailstream.Snapshot{BulkOutstanding: 100000, Retransmissions: 1}, 100000)
	if !e.congested || !e.volatile || e.tail != 400 {
		t.Fatalf("tail/delivery evidence ignored: %+v", e)
	}
	h = bearerHistory{}
	e = h.observe([]float64{30, 40, 400}, 40, mailstream.Snapshot{}, 0)
	if e.congested {
		t.Fatal("idle tail treated as congestion")
	}
}
func TestCellularMinimumAndStaleEvidence(t *testing.T) {
	var p cellularPolicy
	at := time.Unix(100, 0)
	if n := p.decideRecovery(8, 40, true, 10000, at); n != 8 {
		t.Fatal(n)
	}
	for i := 1; i <= 90; i++ {
		if n := p.decideRecovery(8, 1200, true, 0, at.Add(time.Duration(i)*time.Second)); n < 3 {
			t.Fatal(n)
		}
	}
	if p.limit != 3 {
		t.Fatal(p.limit)
	}
	for i := 91; i <= 100; i++ {
		if n := p.decideRecovery(8, 0, true, 100000, at.Add(time.Duration(i)*time.Second)); n != 3 {
			t.Fatal(n)
		}
	}
	for i := 101; i <= 160; i++ {
		p.decideRecovery(8, 50, true, 100000, at.Add(time.Duration(i)*time.Second))
	}
	if p.limit != 8 {
		t.Fatal(p.limit)
	}
}
