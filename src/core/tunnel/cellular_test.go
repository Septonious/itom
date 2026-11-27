package tunnel

import (
	"testing"
	"time"
)

func TestCellularPolicy(t *testing.T) {
	now := time.Now()
	var p cellularPolicy
	for i := 0; i < 60; i++ {
		if n := p.decide(8, 400*time.Millisecond, 5, false, now.Add(time.Duration(i)*time.Second)); n != 8 {
			t.Fatal("healthy idle shrank", n)
		}
	}
	now = now.Add(time.Minute)
	if n := p.decide(8, 401*time.Millisecond, 5, true, now); n != 7 {
		t.Fatal(n)
	}
	if n := p.decide(8, 650*time.Millisecond, 5, true, now.Add(time.Second)); n != 7 {
		t.Fatal("overlapping reduction", n)
	}
	now = now.Add(10 * time.Second)
	if n := p.decide(8, 650*time.Millisecond, 5, true, now); n != 4 {
		t.Fatal(n)
	}
	now = now.Add(10 * time.Second)
	if n := p.decide(8, 700*time.Millisecond, 5, true, now); n != 2 {
		t.Fatal(n)
	}
	now = now.Add(10 * time.Second)
	if n := p.decide(8, 850*time.Millisecond, 5, true, now); n != 1 {
		t.Fatal(n)
	}
	if n := p.decide(8, 50*time.Millisecond, 0, true, now); n != 1 {
		t.Fatal("missing measurement", n)
	}
	for i := 0; i < 10; i++ {
		p.decide(8, 250*time.Millisecond, 5, true, now.Add(time.Duration(10+i)*time.Second))
	}
	if p.limit != 8 {
		t.Fatal("no recovery", p.limit)
	}
	if n := p.decide(5, 0, 0, false, now.Add(time.Minute)); n != 5 {
		t.Fatal(n)
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
