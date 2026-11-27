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
	}{{20 * time.Millisecond, 12 * time.Millisecond},
		{50 * time.Millisecond, 16 * time.Millisecond},
		{90 * time.Millisecond, 24 * time.Millisecond}} {
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
	// Receiving DATA is sufficient: there need not be outgoing DATA.
	now := time.Now()
	var inbound inboundBulkMeter
	if inbound.observe(4096, now) || inbound.observe(64<<10, now) || inbound.observe(64<<10, now.Add(101*time.Millisecond)) {
		t.Fatal("light or separated DATA marked as sustained bulk")
	}
	if !inbound.observe(64<<10, now.Add(102*time.Millisecond)) {
		t.Fatal("sustained inbound DATA not detected")
	}
	link.lastInboundBulk.Store(now.UnixNano())
	if d := link.feedbackCoalesceDelay(); d != 16*time.Millisecond {
		t.Fatalf("inbound-only bulk feedback delay = %v", d)
	}
	link.lastInboundBulk.Store(now.Add(-time.Second).UnixNano())
	if d := link.feedbackCoalesceDelay(); d != 2*time.Millisecond {
		t.Fatalf("inbound idle feedback delay = %v", d)
	}
}

func TestWiFiFeedbackAndRecoveryUseDataWriter(t *testing.T) {
	l := &IMAPLink{}
	for _, mobile := range []bool{false, true, false} {
		l.cellularBearer.Store(mobile)
		if l.useDataPublisher(mailWork{control: true}) {
			t.Fatal("interactive control was diverted")
		}
		for _, work := range []mailWork{{control: true, feedback: true}, {control: true, recovery: true}} {
			if l.useDataPublisher(work) == mobile {
				t.Fatal("incorrect bearer routing", mobile, work)
			}
		}
	}
}
