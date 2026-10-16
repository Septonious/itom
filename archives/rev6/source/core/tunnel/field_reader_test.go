package tunnel

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"itom.local/core/mailbox"
)

type fieldEmptyMailbox struct {
	mailbox.Transport
	polls atomic.Int64
	waits atomic.Int64
}

func (m *fieldEmptyMailbox) WaitReadWake(ctx context.Context, _ string, d time.Duration, wake <-chan struct{}) (bool, bool, bool, error) {
	m.waits.Add(1)
	return true, false, false, readerPause(ctx, wake, d)
}

func (m *fieldEmptyMailbox) Page(ctx context.Context, _ string, cur mailbox.Cursor) ([]mailbox.Message, mailbox.Cursor, error) {
	m.polls.Add(1)
	if err := pause(ctx, 16*time.Millisecond); err != nil {
		return nil, cur, err
	}
	return nil, cur, nil
}

// The field run performed thousands of empty FETCHes while streams were open.
// Active application demand must not disable empty-mailbox pacing; explicit
// response wakeups still bypass the pacing delay without a reconnect.
func TestFieldEmptyActiveReaderBoundsCommandPressureAndWakes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := &fieldEmptyMailbox{}
	l := &lane{ctx: ctx, mail: m, pages: make(chan pageResult, 224), readWake: make(chan struct{}, 1)}
	l.active.Store(1)
	l.startReader()
	defer func() { cancel(); l.workers.Wait() }()
	if err := pause(ctx, 1200*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if polls := m.polls.Load(); polls > 9 || m.waits.Load() == 0 {
		t.Fatalf("empty active reader issued %d FETCHes/1.2s, IDLE waits=%d; budget9", polls, m.waits.Load())
	}
	before := m.polls.Load()
	l.wakeReader()
	deadline := time.NewTimer(100 * time.Millisecond)
	defer deadline.Stop()
	for m.polls.Load() == before {
		select {
		case <-deadline.C:
			t.Fatal("explicit wake did not bypass empty backoff")
		case <-time.After(time.Millisecond):
		}
	}
}
