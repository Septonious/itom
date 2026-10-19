package tunnel

import (
	"itom.local/core/journal"
	"testing"
	"time"
)

func TestRC11HistoricalRecoveryBackoffResetsOnDurableProgress(t *testing.T) {
	now := time.Now()
	g := journal.Grant{}
	r := grantRecoveryRetry{}
	for i, delay := range []time.Duration{15, 30, 60, 120, 240, 300, 300} {
		if !r.due(g, now, 15*time.Second) {
			t.Fatal("initial/due retry suppressed", i)
		}
		r = r.sentFor(g, now)
		delay *= time.Second
		if r.due(g, now.Add(delay-time.Nanosecond), 15*time.Second) {
			t.Fatal("retry storm", i)
		}
		now = now.Add(delay)
	}
	progressed := g
	progressed.Used.Bytes = 1000
	progressed.Used.Messages = 1
	if !r.due(progressed, r.sent.Add(time.Second), 15*time.Second) {
		t.Fatal("progress failed to reopen reconciliation")
	}
	r = r.sentFor(progressed, now)
	if r.attempts != 1 || !r.due(progressed, now.Add(15*time.Second), 15*time.Second) {
		t.Fatal("progress failed to reset backoff", r)
	}
	if r.due(progressed, now.Add(30*time.Second), 45*time.Second) {
		t.Fatal("congestion floor ignored")
	}
	if !r.due(progressed, now.Add(45*time.Second), 45*time.Second) {
		t.Fatal("lost response stopped recovery")
	}
	if g.Used.Bytes != 0 || g.Closed {
		t.Fatal("retry mutated debt")
	}
}
