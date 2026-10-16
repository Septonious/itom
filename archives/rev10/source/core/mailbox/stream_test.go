package mailbox

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

type blockedTailFetch struct {
	imapserver.Session
	release <-chan struct{}
	raw     []byte
	mode    string
}

func (s blockedTailFetch) Fetch(w *imapserver.FetchWriter, set imap.NumSet, opts *imap.FetchOptions) error {
	writeRow := func(uid uint32, size int64) error {
		m := w.CreateMessage(uid)
		m.WriteUID(imap.UID(uid))
		m.WriteRFC822Size(size)
		body := m.WriteBodySection(opts.BodySection[0], int64(len(s.raw)))
		if _, err := body.Write(s.raw); err != nil {
			return err
		}
		if err := body.Close(); err != nil {
			return err
		}
		return m.Close()
	}
	if err := writeRow(1, int64(len(s.raw))); err != nil {
		return err
	}
	<-s.release
	switch s.mode {
	case "tagged-error":
		return &imap.Error{Type: imap.StatusResponseTypeNo, Text: "FETCH rejected after first row"}
	case "bad-next-body":
		return writeRow(2, int64(len(s.raw)+1))
	default:
		return writeRow(2, int64(len(s.raw)))
	}
}

func TestTailStreamDeliversBeforeTaggedCompletionWithoutCommittingFailedCursor(t *testing.T) {
	for _, mode := range []string{"success", "tagged-error", "bad-next-body", "callback-cancel"} {
		t.Run(mode, func(t *testing.T) {
			raw, err := Encode([]byte("latency-critical"))
			if err != nil {
				t.Fatal(err)
			}
			release := make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			cfg, tc := testServerWithSession(t, "tls", true, func(s imapserver.Session) imapserver.Session {
				return blockedTailFetch{s, release, raw, mode}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c, err := dial(ctx, cfg, []string{"stream"}, tc)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if err := c.Ensure(ctx, "stream"); err != nil {
				t.Fatal(err)
			}
			first := make(chan Message, 2)
			type result struct {
				rows []Message
				next Cursor
				err  error
			}
			done := make(chan result, 1)
			original := Cursor{Validity: 424242}
			callbackErr := errors.New("reader canceled delivery")
			go func() {
				rows, next, err := c.PageTailStream(ctx, "stream", original, true, 4, func(m Message) error {
					first <- m
					if mode == "callback-cancel" {
						return callbackErr
					}
					return nil
				})
				done <- result{rows, next, err}
			}()
			select {
			case row := <-first:
				if row.UID != 1 || row.Validity == 0 || row.Validity == original.Validity || row.Folder != "stream" || string(row.MIME) != string(raw) {
					t.Fatal("provisional row lacks complete scoped body", row)
				}
			case <-ctx.Done():
				t.Fatal("first complete row was held behind FETCH completion")
			}
			if mode != "callback-cancel" {
				select {
				case got := <-done:
					t.Fatal("cursor committed before command completion", got)
				default:
				}
			}
			once.Do(func() { close(release) })
			select {
			case got := <-done:
				if mode == "success" {
					if got.err != nil || len(got.rows) != 2 || got.next.After != 2 {
						t.Fatal(got)
					}
				} else if got.err == nil || len(got.rows) != 0 || got.next != original {
					t.Fatal(fmt.Sprintf("%s committed failed FETCH: %+v", mode, got))
				} else if mode == "callback-cancel" && !errors.Is(got.err, callbackErr) {
					t.Fatal(got.err)
				}
			case <-ctx.Done():
				t.Fatal("failed FETCH did not terminate")
			}
		})
	}
}

func TestTailStreamPartialUpdateDoesNotStealOrDuplicateSolicitedBody(t *testing.T) {
	var wrapped *partialUpdateBeforeFullFetch
	cfg, tc := testServerWithSession(t, "tls", true, func(s imapserver.Session) imapserver.Session {
		wrapped = &partialUpdateBeforeFullFetch{Session: s}
		return wrapped
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := dial(ctx, cfg, []string{"stream-retry"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Ensure(ctx, "stream-retry"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		raw, err := Encode([]byte(fmt.Sprintf("row-%d", i)))
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Append(ctx, "stream-retry", raw); err != nil {
			t.Fatal(err)
		}
	}
	wrapped.arm.Store(true)
	counts := make(map[uint32]int)
	rows, next, err := c.PageTailStream(ctx, "stream-retry", Cursor{}, true, 4, func(m Message) error {
		counts[m.UID]++
		return nil
	})
	if err != nil || len(rows) != 2 || next.After != 2 || counts[1] != 1 || counts[2] != 1 {
		t.Fatal("partial update lost or duplicated a complete BODY", counts, rows, next, err)
	}
	if c.partialRetries.Load() != 0 {
		t.Fatal("partial update forced a redundant full FETCH", c.partialRetries.Load())
	}
}
