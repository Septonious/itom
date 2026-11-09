package mailstream

import (
	"sync/atomic"
	"testing"
	"time"
)

// RTT is a hard gate for RC2: no quiet-state delay beyond the released RC1.
func TestRC117PollingNeverSlowsFirstPacket(t *testing.T) {
	for _, quiet := range []time.Duration{
		0, time.Millisecond, 1500*time.Millisecond, 2*time.Second,
		9*time.Second, 10*time.Second, 29*time.Second,
		30*time.Second, time.Minute, 24*time.Hour,
	} {
		if got := controlPollInterval(quiet); got != time.Millisecond {
			t.Fatalf("quiet=%v, polling regressed from RC1: %v", quiet, got)
		}
	}
	var activity atomic.Int64
	p := &mailPipe{pollTraffic: &activity}
	now := time.Now()
	for _, offset := range []time.Duration{
		-24*time.Hour, -time.Minute, -30*time.Second,
		-2*time.Second, 0, time.Second, 24*time.Hour,
	} {
		activity.Store(now.Add(offset).UnixNano())
		if got := p.controlPollDelay(now); got != time.Millisecond {
			t.Fatalf("activity=%v, first-packet RTT regression: %v", offset, got)
		}
	}
	if got := (&mailPipe{}).controlPollDelay(now); got != time.Millisecond {
		t.Fatalf("nil activity clock changed RC1 polling: %v", got)
	}
}
