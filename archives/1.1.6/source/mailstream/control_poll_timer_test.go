package mailstream

import (
	"testing"
	"time"
)

// A single control FETCH timer must never deliver a stale tick after a
// command woke the reader and canceled the pending empty-poll wait.
func TestRC117ControlPollTimerResetAfterCommand(t *testing.T) {
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	for i := 0; i < 1000; i++ {
		timer.Reset(time.Second)
		if !timer.Stop() {
			t.Fatal("pending poll unexpectedly fired before cancellation")
		}
		select {
		case <-timer.C:
			t.Fatal("stale poll tick delivered after timer cancellation")
		default:
		}
	}
	if controlPollInterval(time.Hour) != time.Millisecond {
		t.Fatal("idle control poll delay changed from field-proven RC1")
	}
	timer.Reset(time.Millisecond)
	select {
	case <-timer.C:
	case <-time.After(time.Second):
		t.Fatal("reused poll timer did not trigger")
	}
}

// Steady-state reset/stop does not need a new heap timer per empty FETCH.
// Benchmark allocation count independently of IMAP/provider service time.
func BenchmarkRC117ControlPollTimerReuse(b *testing.B) {
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		timer.Reset(time.Second)
		timer.Stop()
	}
}
