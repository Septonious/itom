package tunnel

import (
	"testing"
	"time"
)

// Physical APPEND completion alone must not open this path. Only the existing
// authenticated peer BODY receipt can train service; slow paths must fall back.
func TestRC5ColdRampNeedsAuthenticatedPromptBodyAndRollsBack(t *testing.T) {
	l := &lane{side: true}
	if got := l.serviceBatchTarget(); got != 32<<10 {
		t.Fatal("missing cold floor", got)
	}
	now := time.Now()
	l.observePeerReadService(32<<10, now.Add(-200*time.Millisecond), now)
	if got := l.serviceBatchTarget(); got != 64<<10 {
		t.Fatal("first verified BODY failed to graduate", got)
	}
	l.observePeerReadService(32<<10, now.Add(-100*time.Millisecond), now)
	if got := l.serviceBatchTarget(); got != 64<<10 {
		t.Fatal("one extra receipt expanded more than one tier", got)
	}
	l.observePeerReadService(32<<10, now.Add(-100*time.Millisecond), now)
	if got := l.serviceBatchTarget(); got != 128<<10 {
		t.Fatal("further proven progress did not expand", got)
	}
	for i := 0; i < 2; i++ {
		l.observePeerReadService(128<<10, now.Add(-12*time.Second), now)
	}
	if got := l.serviceBatchTarget(); got != 32<<10 {
		t.Fatal("slow BODY must restore conservative batch", got)
	}
	slow := &lane{side: true}
	slow.observePeerReadService(32<<10, now.Add(-1500*time.Millisecond), now)
	if got := slow.serviceBatchTarget(); got != 32<<10 {
		t.Fatal("slow first receipt cannot accelerate", got)
	}
	empty := &lane{side: true}
	empty.observePeerReadService(512, now.Add(-50*time.Millisecond), now)
	if got := empty.serviceBatchTarget(); got != 32<<10 {
		t.Fatal("short receipt cannot accelerate", got)
	}
}

// A small, fully utilized logical window may probe faster after fresh useful
// ACKs, but never on empty ticks, ACK-only drained flight, or RTT pressure.
func TestRC5ColdWindowAcceleratesOnlyOnVerifiedHealthyACK(t *testing.T) {
	now := time.Unix(10000, 0)
	feedback := deliveryFeedback{Window: deliveryInitialWindow, Flight: deliveryInitialWindow,
		ACKFlight: deliveryInitialWindow, Delivered: 512 << 10, Demand: true,
		Age: 300 * time.Millisecond, Target: deliveryTargetAge, Samples: 1}
	p := deliveryPolicy{}
	first := p.next(now, feedback)
	if first != 2*deliveryInitialWindow {
		t.Fatalf("verified cold ramp=%d", first)
	}
	feedback.Window = first
	feedback.Flight = first
	feedback.ACKFlight = first
	feedback.Delivered += 512 << 10
	second := p.next(now.Add(time.Second), feedback)
	if second != 4*deliveryInitialWindow {
		t.Fatalf("second healthy ramp=%d", second)
	}
	f := feedback
	f.Window, f.Flight, f.ACKFlight = deliveryInitialWindow, 0, deliveryInitialWindow
	f.Delivered, f.Samples = 0, 0
	p = deliveryPolicy{}
	if w := p.next(now, f); w > deliveryInitialWindow {
		t.Fatal("no useful ACK invented flight", w)
	}
	f.Delivered, f.Samples, f.Flight = 512<<10, 1, 0
	p = deliveryPolicy{}
	if w := p.next(now, f); w > deliveryInitialWindow+deliveryInitialWindow/2 {
		t.Fatal("drained ACK inflated flight", w)
	}
	f.Flight = deliveryInitialWindow
	f.Age = 750 * time.Millisecond
	p = deliveryPolicy{}
	if w := p.next(now, f); w > deliveryInitialWindow+deliveryInitialWindow/2 {
		t.Fatal("slow receipt opened fast ramp", w)
	}
	f.Age, f.Pressure = 300*time.Millisecond, true
	p = deliveryPolicy{}
	if w := p.next(now, f); w >= deliveryInitialWindow {
		t.Fatal("fast-path pressure not respected", w)
	}
}
