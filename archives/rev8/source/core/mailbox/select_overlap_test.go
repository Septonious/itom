package mailbox

import (
	"context"
	"errors"
	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSelectedTailRenewalOverlapsUIDFetchButWaitsForSelection(t *testing.T) {
	var armed atomic.Bool
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	cfg, tc := testServerWithSession(t, "tls", true, func(s imapserver.Session) imapserver.Session { return &snapshotSelect{s, &armed, entered, release} })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, err := dial(ctx, cfg, []string{"renewal"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = c.Ensure(ctx, "renewal"); err != nil {
		t.Fatal(err)
	}
	cur, err := c.Tail(ctx, "renewal")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := Encode([]byte("ready"))
	if err = c.Append(ctx, "renewal", raw); err != nil {
		t.Fatal(err)
	}
	_, before, _ := c.ReadCommandCounts()
	armed.Store(true)
	delivered := make(chan Message, 1)
	done := make(chan error, 1)
	go func() {
		_, _, err := c.PageTailStream(ctx, "renewal", cur, true, 4, func(m Message) error { delivered <- m; return nil })
		done <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("renewal not entered")
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		_, after, _ := c.ReadCommandCounts()
		if after > before {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("FETCH waited for renewal round trip")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-delivered:
		t.Fatal("row exposed before selection proof")
	default:
	}
	once.Do(func() { close(release) })
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	m := <-delivered
	if m.Validity != cur.Validity || string(m.MIME) != string(raw) {
		t.Fatal("row scope or integrity lost")
	}
}

type changedRenewal struct {
	imapserver.Session
	mode *atomic.Int32
}

func (s *changedRenewal) Select(folder string, opts *imap.SelectOptions) (*imap.SelectData, error) {
	d, err := s.Session.Select(folder, opts)
	if err != nil {
		return d, err
	}
	if s.mode.Load() == 2 {
		return nil, &imap.Error{Type: imap.StatusResponseTypeNo, Text: "renewal failed"}
	}
	if s.mode.Load() == 1 {
		copy := *d
		copy.UIDValidity++
		return &copy, nil
	}
	return d, nil
}
func TestSelectedTailRenewalRejectsOldIncarnationAndFailedSelection(t *testing.T) {
	for _, mode := range []int32{1, 2} {
		t.Run(map[int32]string{1: "changed", 2: "failed"}[mode], func(t *testing.T) {
			var state atomic.Int32
			cfg, tc := testServerWithSession(t, "tls", true, func(s imapserver.Session) imapserver.Session { return &changedRenewal{s, &state} })
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			c, err := dial(ctx, cfg, []string{"incarnation"}, tc)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if err = c.Ensure(ctx, "incarnation"); err != nil {
				t.Fatal(err)
			}
			cur, err := c.Tail(ctx, "incarnation")
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := Encode([]byte("new identity"))
			if err = c.Append(ctx, "incarnation", raw); err != nil {
				t.Fatal(err)
			}
			state.Store(mode)
			var delivered []Message
			rows, next, err := c.PageTailStream(ctx, "incarnation", cur, true, 4, func(m Message) error { delivered = append(delivered, m); return nil })
			if mode == 1 {
				if err != nil || len(rows) != 1 || len(delivered) != 1 || next.Validity != cur.Validity+1 || delivered[0].Validity != next.Validity {
					t.Fatal("old incarnation exposed or new page lost", rows, next, delivered, err)
				}
			} else {
				var status *imap.Error
				if !errors.As(err, &status) || len(rows) != 0 || len(delivered) != 0 || next != cur {
					t.Fatal("failed SELECT admitted rows", rows, next, delivered, err)
				}
			}
		})
	}
}
