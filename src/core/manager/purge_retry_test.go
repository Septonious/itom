package manager

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"itom.local/core/mailbox"
)

type purgeRetryAdmin struct {
	report mailbox.PurgeReport
	err    error
	closed bool
	calls  int
}

func (a *purgeRetryAdmin) Folders(context.Context) ([]mailbox.Folder, error) { return nil, nil }
func (a *purgeRetryAdmin) PurgeAll(context.Context) (mailbox.PurgeReport, error) {
	a.calls++
	return a.report, a.err
}
func (a *purgeRetryAdmin) Close() error { a.closed = true; return nil }
func TestPurgeRetryFreshConnection(t *testing.T) {
	for _, success := range []bool{true, false} {
		u := New(t.TempDir())
		u.Out = io.Discard
		u.stoppedCheck = func() error { return nil }
		u.pauseAdmin = func(context.Context, time.Duration) error { return nil }
		first := &purgeRetryAdmin{err: io.ErrUnexpectedEOF}
		second := &purgeRetryAdmin{err: context.DeadlineExceeded}
		third := &purgeRetryAdmin{report: mailbox.PurgeReport{Verified: success}}
		if !success {
			third.err = errors.New("provider refused")
		}
		attempts := []*purgeRetryAdmin{first, second, third}
		n := 0
		u.adminDial = func(context.Context, mailbox.Config) (accountAdmin, error) {
			if n >= len(attempts) {
				t.Fatal("unbounded retry")
			}
			if n > 0 && !attempts[n-1].closed {
				t.Fatal("overlapping admin sockets")
			}
			a := attempts[n]
			n++
			return a, nil
		}
		err := u.purgeAccountRetry(context.Background(), mailbox.Config{Username: "fixture"})
		if (err == nil) != success || n != 3 {
			t.Fatalf("success=%v err=%v attempts=%d", success, err, n)
		}
		for _, a := range attempts {
			if !a.closed || a.calls != 1 {
				t.Fatal("ambiguous connection reused or leaked")
			}
		}
	}
}
