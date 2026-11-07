package mailstream

import (
	"testing"
	"time"
)

func TestControlProviderTailRequiresPersistentQueueEvidence(t *testing.T) {
	now := time.Unix(100, 0)
	w := deliveryWindow{adaptive: true, limit: 8 << 20, rate: 10 << 20}
	w.observeInteractive(100*time.Millisecond, now)
	w.observeInteractive(900*time.Millisecond, now.Add(time.Second))
	w.observeInteractive(900*time.Millisecond, now.Add(time.Second+50*time.Millisecond))
	if w.pacer.guarded || w.allowance() != 8<<20 {
		t.Fatal("one compressed callback burst erased the measured bulk allowance")
	}
	w.observeInteractive(100*time.Millisecond, now.Add(time.Second+75*time.Millisecond))
	w.observeInteractive(900*time.Millisecond, now.Add(2*time.Second))
	if w.pacer.guarded {
		t.Fatal("independent provider tails accumulated as a persistent queue")
	}
	w.observeInteractive(900*time.Millisecond, now.Add(2100*time.Millisecond))
	if !w.pacer.guarded || !now.Add(2100*time.Millisecond).Before(w.controlSlowUntil) || w.allowance() >= 8<<20 {
		t.Fatal("persistent shared-link delay did not protect interactive traffic")
	}
	w = deliveryWindow{adaptive: true, controlBase: 100 * time.Millisecond}
	w.observeInteractive(2*time.Second, now)
	if !w.pacer.guarded {
		t.Fatal("severe delay waited for a second callback")
	}
}
