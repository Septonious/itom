package tunnel

import (
	"context"
	"itom.local/core/wire"
	"testing"
	"time"
)

func TestReaderIdleAuditDelayLimitedPostHotGrace(t *testing.T) {
	now := time.Now()
	deadline := now.Add(-100 * time.Millisecond).UnixNano()
	for _, active := range []int64{0, 1, 100} {
		if got := readerIdleAuditDelay(active, deadline, now); got != 150*time.Millisecond {
			t.Fatalf("recent response hint must get a short audit, active=%d, got=%v", active, got)
		}
	}
	for _, tc := range []struct {
		active int64
		want   time.Duration
	}{
		{0, 2 * time.Second}, {1, 500 * time.Millisecond}, {10, 500 * time.Millisecond},
	} {
		if got := readerIdleAuditDelay(tc.active, 0, now); got != tc.want {
			t.Fatalf("no hint: active=%d got=%v", tc.active, got)
		}
		if got := readerIdleAuditDelay(tc.active, now.Add(-time.Second).UnixNano(), now); got != tc.want {
			t.Fatalf("stale hint: active=%d got=%v", tc.active, got)
		}
		if got := readerIdleAuditDelay(tc.active, now.Add(time.Second).UnixNano(), now); got != tc.want {
			t.Fatalf("future hint: active=%d got=%v", tc.active, got)
		}
	}
}

func TestRefreshReaderAfterPingAppendOnly(t *testing.T) {
	l := &lane{ctx: context.Background(), readWake: make(chan struct{}, 1)}
	l.refreshReaderAfterPingAppend([]queuedControl{{Frame: wire.Frame{Type: wire.Pong}}})
	if got := l.readHotGeneration.Load(); got != 0 {
		t.Fatalf("PONG rearmed reader: %d", got)
	}
	l.refreshReaderAfterPingAppend([]queuedControl{{Frame: wire.Frame{Type: wire.Ping}}})
	if got := l.readHotGeneration.Load(); got != 1 {
		t.Fatalf("PING didn't rearm reader: %d", got)
	}
	if l.readHotUntil.Load() <= time.Now().UnixNano() {
		t.Fatal("PING hot hint already expired")
	}
	select {
	case <-l.readWake:
	default:
		t.Fatal("PING didn't wake reader")
	}
	l.refreshReaderAfterPingAppend([]queuedControl{{Frame: wire.Frame{Type: wire.OpenOK}}})
	if l.readHotGeneration.Load() != 1 {
		t.Fatal("reply rearmed reader")
	}
}
