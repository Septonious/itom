package tunnel

import (
	"testing"
	"time"
)

func TestRev4HealthyProgressDoesNotTreatVisibilityTailAsGlobalQueue(t *testing.T) {
	now := time.Now()
	p := deliveryPolicy{lastProgress: now.Add(-100 * time.Millisecond)}
	f := deliveryFeedback{Window: 1 << 20, InitialWindow: 1 << 20, Flight: 256 << 10,
		Delivered: 32 << 10, Demand: true, Samples: 8, Age: 2200 * time.Millisecond,
		FastAge: 200 * time.Millisecond, Target: 800 * time.Millisecond, FastTarget: 650 * time.Millisecond}
	window := p.next(now, f)
	if window < f.Window || p.mode == deliveryCongested {
		t.Fatal("healthy byte progress and short path collapsed behind one visibility tail", window, p.mode)
	}
	f.Window = window
	f.Samples = 0
	f.Age += 1500 * time.Millisecond
	if reduced := p.next(now.Add(1500*time.Millisecond), f); reduced >= window {
		t.Fatal("visibility discovery bypassed a real progress stall", reduced)
	}
}
