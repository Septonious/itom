package tunnel

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"itom.local/core/mailbox"
)

type hintEpochMailbox struct {
	mailbox.Transport
	entered chan struct{}
	starts  chan time.Time
	fresh   atomic.Bool
}

func (m *hintEpochMailbox) Page(ctx context.Context, _ string, cur mailbox.Cursor) ([]mailbox.Message, mailbox.Cursor, error) {
	if m.fresh.Load() {
		select {
		case m.starts <- time.Now():
		default:
		}
	}
	return nil, cur, pause(ctx, 100*time.Millisecond)
}

func (m *hintEpochMailbox) WaitReadWake(ctx context.Context, _ string, d time.Duration, wake <-chan struct{}) (bool, bool, bool, error) {
	select {
	case m.entered <- struct{}{}:
	default:
	}
	return true, false, false, readerPause(ctx, wake, d)
}

// An actual reader enters IDLE after empty polls, then receives a new response
// hint. Its first empty recheck must not inherit the previous quiet backoff.
func TestReaderFreshHintRechecksBeforeLongEmptyBackoff(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	m := &hintEpochMailbox{entered: make(chan struct{}, 1), starts: make(chan time.Time, 4)}
	l := &lane{ctx: ctx, mail: m, pages: make(chan pageResult, 32), readWake: make(chan struct{}, 1)}
	l.active.Store(1)
	l.startReader()
	defer func() { cancel(); l.workers.Wait() }()
	select {
	case <-m.entered:
	case <-ctx.Done():
		t.Fatal("reader did not enter quiet IDLE")
	}
	m.fresh.Store(true)
	l.armReaderHot(time.Second)
	var first, second time.Time
	select {
	case first = <-m.starts:
	case <-ctx.Done():
		t.Fatal("hint failed to wake reader")
	}
	select {
	case second = <-m.starts:
	case <-ctx.Done():
		t.Fatal("hint failed to recheck empty tail")
	}
	// 100ms simulated command + fresh10ms pause, with scheduler headroom.
	// Retaining the quiet streak adds40ms or80ms instead.
	if elapsed := second.Sub(first); elapsed >= 135*time.Millisecond {
		t.Fatalf("fresh response inherited quiet polling streak: %s", elapsed)
	}
}
