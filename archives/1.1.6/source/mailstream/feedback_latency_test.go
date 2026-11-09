package mailstream

import (
	"testing"
	"time"
)

func TestRC116FeedbackCoalescingAdaptsToIMAPLatency(t *testing.T) {
	link := &IMAPLink{}
	if d := link.feedbackCoalesceDelay(); d != 2*time.Millisecond {
		t.Fatalf("idle feedback delay = %v", d)
	}
	link.lastBulk.Store(time.Now().UnixNano())
	if d := link.feedbackCoalesceDelay(); d != 16*time.Millisecond {
		t.Fatalf("unmeasured loaded feedback delay = %v", d)
	}
	p := &mailPipe{}
	p.latencyAt.Store(time.Now().UnixNano())
	a := &mailAccountState{control: p}
	link.accounts = []*mailAccountState{a}
	for _, pair := range []struct {
		rtt, want time.Duration
	}{{20*time.Millisecond, 12*time.Millisecond},
		{50*time.Millisecond, 16*time.Millisecond},
		{90*time.Millisecond, 24*time.Millisecond}} {
		p.latency.Store(int64(pair.rtt))
		if d := link.feedbackCoalesceDelay(); d != pair.want {
			t.Fatalf("observed IMAP RTT %v: feedback delay=%v want=%v", pair.rtt, d, pair.want)
		}
	}
	p.latencyAt.Store(time.Now().Add(-time.Minute).UnixNano())
	if d := link.feedbackCoalesceDelay(); d != 16*time.Millisecond {
		t.Fatalf("stale sample should use conservative 16ms, got %v", d)
	}
	link.lastBulk.Store(time.Now().Add(-time.Second).UnixNano())
	if d := link.feedbackCoalesceDelay(); d != 2*time.Millisecond {
		t.Fatalf("idle-after-bulk delay = %v", d)
	}
}
