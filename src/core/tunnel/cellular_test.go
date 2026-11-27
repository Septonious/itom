package tunnel

import (
	"testing"
	"time"
)

func TestCellularPolicy(t *testing.T) {
	now := time.Now()
	var p cellularPolicy
	for i := 0; i < 60; i++ {
		if n := p.decide(30, 400*time.Millisecond, 5, false, now.Add(time.Duration(i)*time.Second)); n != 30 {
			t.Fatal(n)
		}
	}
	now = now.Add(time.Minute)
	for _, v := range []struct {
		ms   int
		want int
	}{{401, 27}, {650, 21}, {700, 10}, {850, 4}, {250, 4}, {250, 10}, {250, 10}, {250, 16}} {
		if got := p.decide(30, time.Duration(v.ms)*time.Millisecond, 5, true, now); got != v.want {
			t.Fatalf("%dms: got %d want %d", v.ms, got, v.want)
		}
		if v.ms > 400 && v.want > 1 {
			if got := p.decide(30, 900*time.Millisecond, 5, true, now.Add(time.Millisecond)); got != v.want {
				t.Fatalf("overlapping reduction %d", got)
			}
		}
		now = now.Add(10 * time.Second)
	}
}
func TestCellularPercentagesAndBaseline(t *testing.T) {
	for _, v := range []struct{ n, p, want int }{{20, 35, 7}, {25, 35, 9}, {30, 35, 11}, {20, 20, 4}, {25, 10, 3}, {30, 20, 6}, {1, 10, 1}} {
		if got := accountStep(v.n, v.p); got != v.want {
			t.Fatal(v, got)
		}
	}
	var b cellularBaseline
	for _, v := range []int{156, 167, 179, 191, 1000} {
		b.add(time.Duration(v) * time.Millisecond)
	}
	base, variation := b.estimate()
	if base != 179*time.Millisecond || variation < 17*time.Millisecond || variation > 19*time.Millisecond {
		t.Fatal(base, variation)
	}
}
func TestCellularRTTWindowAndBearerReset(t *testing.T) {
	now := time.Now()
	c := &Client{networkType: "cellular", rttHistory: []rttSample{{now.Add(-10 * time.Second), 5 * time.Second}, {now.Add(-9 * time.Second), 100 * time.Millisecond}, {now.Add(-5 * time.Second), 800 * time.Millisecond}, {now, 200 * time.Millisecond}}}
	avg, n, epoch, mobile := c.cellularRTT(now)
	if !mobile || n != 3 || avg != 1100*time.Millisecond/3 {
		t.Fatalf("average %v samples %d", avg, n)
	}
	c.SetNetworkType("cellular")
	if _, n, e, _ := c.cellularRTT(now); n != 3 || e != epoch {
		t.Fatal("identical bearer reset")
	}
	c.SetNetworkType("wifi")
	if _, n, e, _ := c.cellularRTT(now); n != 0 || e != epoch+1 {
		t.Fatal("bearer history retained")
	}
	if n := (&cellularPolicy{}).decide(8, 800*time.Millisecond, 1, true, now); n != 8 {
		t.Fatal("one spike cut accounts", n)
	}
}

func TestCellularFourAccountMinimum(t *testing.T) {
	now := time.Now()
	for _, capacity := range []int{4, 5, 8, 20, 25, 30} {
		p := cellularPolicy{}
		for i := 0; i < 20; i++ {
			got := p.decide(capacity, 5*time.Second, 5, true, now.Add(time.Duration(i)*cellularRTTWindow))
			if got < 4 || got > capacity {
				t.Fatalf("capacity=%d got=%d", capacity, got)
			}
		}
		if p.limit != 4 {
			t.Fatalf("capacity=%d floor=%d", capacity, p.limit)
		}
	}
	for n := 0; n < 4; n++ {
		if got := (&cellularPolicy{}).decide(n, time.Second, 5, true, now); got != 0 {
			t.Fatalf("invalid allocation %d: %d", n, got)
		}
	}
}

func TestRC12CellularStallCannotReduce(t *testing.T) {
	now := time.Now()
	p := cellularPolicy{}
	for i := 0; i < 8; i++ {
		e := cellularEvidence{active: 8, streams: 16, fresh: true, serviceCapacity: 12, publications: 2}
		if got := p.decideObserved(8, 2*time.Second, 5, true, now.Add(time.Duration(i)*10*time.Second), e); got != 8 {
			t.Fatal("idle/stall reduction", got)
		}
	}
	p = cellularPolicy{limit: 4}
	if got := p.decideObserved(8, 5*time.Second, 5, false, now, cellularEvidence{active: 4, streams: 16, timeout: true, queued: 6}); got != 6 {
		t.Fatal("recovery requires expansion", got)
	}
	p = cellularPolicy{limit: 8}
	e := cellularEvidence{active: 8, streams: 16, fresh: true, bulkPressure: true, serviceCapacity: 20, publications: 5}
	if got := p.decideObserved(8, 800*time.Millisecond, 5, true, now, e); got != 5 {
		t.Fatal("bulk reduction", got)
	}
	e.active = 5
	if got := p.decideObserved(8, 900*time.Millisecond, 5, true, now.Add(10*time.Second), e); got != 8 {
		t.Fatal("failed reduction rollback", got)
	}
	e = cellularEvidence{active: 4, streams: 15, fresh: true}
	if canLearnCellularBaseline(e, 200, false, true) || canLearnCellularBaseline(cellularEvidence{fresh: true}, 1519, false, true) {
		t.Fatal("stalled sample learned")
	}
	if !canLearnCellularBaseline(cellularEvidence{active: 8, fresh: true}, 228, false, true) {
		t.Fatal("healthy idle not learned")
	}
}
