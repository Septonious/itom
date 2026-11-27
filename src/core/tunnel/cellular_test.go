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
	}{{401, 27}, {650, 21}, {700, 10}, {850, 1}, {250, 1}, {250, 7}, {250, 7}, {250, 13}} {
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
