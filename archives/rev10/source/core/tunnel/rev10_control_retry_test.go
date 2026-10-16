package tunnel

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"itom.local/core/mailbox"
)

func TestRev10ControlReplacementRetryBoundaries(t *testing.T) {
	t.Run("temporary EOF then success", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		calls := 0
		want := &mailbox.Client{}
		got, err := dialControlReplacement(ctx, func(context.Context) (*mailbox.Client, error) {
			calls++
			if calls == 1 { return nil, io.EOF }
			return want, nil
		})
		if err != nil || got != want || calls != 2 { t.Fatalf("got=%p err=%v calls=%d", got, err, calls) }
	})
	t.Run("permanent failure is not retried", func(t *testing.T) {
		calls := 0
		denied := errors.New("IMAP_AUTHENTICATION_FAILED")
		_, err := dialControlReplacement(context.Background(), func(context.Context) (*mailbox.Client, error) { calls++; return nil, denied })
		if err != denied || calls != 1 { t.Fatalf("err=%v calls=%d", err, calls) }
	})
	t.Run("budget cancels backoff", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		calls := 0
		_, err := dialControlReplacement(ctx, func(context.Context) (*mailbox.Client, error) { calls++; return nil, io.EOF })
		if !errors.Is(err, context.DeadlineExceeded) || calls != 1 { t.Fatalf("err=%v calls=%d", err, calls) }
	})
}
