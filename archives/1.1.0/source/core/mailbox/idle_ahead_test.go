package mailbox

import (
	"context"
	"errors"
	"github.com/emersion/go-imap/v2/imapserver"
	"sync"
	"testing"
	"time"
)

type idleCompletionBarrier struct {
	imapserver.Session
	entered, doneSeen chan struct{}
	release           <-chan struct{}
}

func (s *idleCompletionBarrier) Idle(w *imapserver.UpdateWriter, stop <-chan struct{}) error {
	close(s.entered)
	err := s.Session.Idle(w, stop)
	close(s.doneSeen)
	<-s.release
	return err
}

func TestIdleAheadSendsDoneBeforeNextOperationAndJoinsCompletion(t *testing.T) {
	entered, doneSeen, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	cfg, tc := testServerWithSession(t, "tls", true, func(s imapserver.Session) imapserver.Session {
		return &idleCompletionBarrier{s, entered, doneSeen, release}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, err := dial(ctx, cfg, []string{"ahead"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = c.Ensure(ctx, "ahead"); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Tail(ctx, "ahead"); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case <-c.updates:
		default:
			goto drained
		}
	}
drained:
	wake := make(chan struct{})
	idleDone := make(chan error, 1)
	go func() {
		_, kicked, err := c.IdleWaitWakeAhead(ctx, "ahead", time.Second, wake)
		if err == nil && !kicked {
			err = errors.New("wake not reported")
		}
		idleDone <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("IDLE not entered")
	}
	close(wake)
	select {
	case err := <-idleDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("waited for tagged DONE completion")
	}
	select {
	case <-doneSeen:
	case <-ctx.Done():
		t.Fatal("DONE continuation not sent")
	}
	called, finished := make(chan struct{}), make(chan error, 1)
	go func() { finished <- c.operation(ctx, "ahead", func() error { close(called); return nil }) }()
	select {
	case <-called:
	case <-ctx.Done():
		t.Fatal("following operation was not overlapped")
	}
	select {
	case err := <-finished:
		t.Fatal("exposed result before IDLE completion", err)
	case <-time.After(20 * time.Millisecond):
	}
	unblock()
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if _, err = c.Tail(ctx, "ahead"); err != nil {
		t.Fatal("reader poisoned", err)
	}
}

func TestIdleCompletionFailureDiscardsFetchAndCursor(t *testing.T) {
	cfg, tc := testServerWithSession(t, "tls", true, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c, err := dial(ctx, cfg, []string{"ahead"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = c.Ensure(ctx, "ahead"); err != nil {
		t.Fatal(err)
	}
	cur, err := c.Tail(ctx, "ahead")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := Encode([]byte("must not expose this unconfirmed result"))
	if err = c.Append(ctx, "ahead", raw); err != nil {
		t.Fatal(err)
	}
	pending := make(chan error, 1)
	sentinel := errors.New("provider IDLE NO")
	pending <- sentinel
	c.pendingIdle = &pendingIdleCompletion{done: pending, stop: func() bool { return true }}
	rows, next, err := c.PageTail(ctx, "ahead", cur, false)
	if !errors.Is(err, sentinel) || len(rows) != 0 || next != cur {
		t.Fatal("unconfirmed completion advanced cursor", rows, next, cur, err)
	}
}

// Cancellation must unblock an overlapped tagged completion even if the
// provider never sends its reply. The application cannot consume that FETCH.
func TestIdleAheadCancellationUnblocksPendingCompletion(t *testing.T) {
	entered, doneSeen, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer close(release)
	cfg, tc := testServerWithSession(t, "tls", true, func(s imapserver.Session) imapserver.Session {
		return &idleCompletionBarrier{s, entered, doneSeen, release}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, err := dial(ctx, cfg, []string{"cancel-ahead"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = c.Ensure(ctx, "cancel-ahead"); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Tail(ctx, "cancel-ahead"); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case <-c.updates:
		default:
			goto drainedCancel
		}
	}
drainedCancel:
	wake := make(chan struct{})
	idleDone := make(chan error, 1)
	go func() { _, _, err := c.IdleWaitWakeAhead(ctx, "cancel-ahead", time.Second, wake); idleDone <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("IDLE not entered")
	}
	close(wake)
	select {
	case err := <-idleDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("wake blocked")
	}
	called, finished := make(chan struct{}), make(chan error, 1)
	go func() { finished <- c.operation(ctx, "cancel-ahead", func() error { close(called); return nil }) }()
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("operation blocked")
	}
	cancel()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("unconfirmed result after cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("pending completion ignored cancellation")
	}
}

func TestIdleDoneStallInterruptsAndJoinsBeforeNextCommand(t *testing.T) {
	entered, doneSeen, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer close(release)
	cfg, tc := testServerWithSession(t, "tls", true, func(s imapserver.Session) imapserver.Session {
		return &idleCompletionBarrier{s, entered, doneSeen, release}
	})
	// The receive no-progress watchdog intentionally requires at least eight
	// seconds. The old four-second parent deadline raced the guard instead
	// of exercising its bounded interruption and join.
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	c, err := dial(ctx, cfg, []string{"done-stall"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = c.Ensure(ctx, "done-stall"); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Tail(ctx, "done-stall"); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case <-c.updates:
		default:
			goto drainedStall
		}
	}
drainedStall:
	wake := make(chan struct{})
	result := make(chan error, 1)
	go func() { _, _, err := c.IdleWaitWake(ctx, "done-stall", time.Second, wake); result <- err }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("IDLE not entered")
	}
	begin := time.Now()
	close(wake)
	select {
	case <-doneSeen:
	case <-ctx.Done():
		t.Fatal("DONE not sent")
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("stall outcome", err)
		}
	case <-ctx.Done():
		t.Fatal("DONE not joined")
	}
	if time.Since(begin) > 11*time.Second || c.Ready() {
		t.Fatal("stalled receive still live", time.Since(begin))
	}
	if _, err = c.Tail(ctx, "done-stall"); err == nil {
		t.Fatal("next command admitted to old receiver")
	}
}
