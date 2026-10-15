package mailbox

import (
	"context"
	"errors"
	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"sync/atomic"
	"testing"
	"time"
)

// Emulates queued UID+FLAGS notifications for a message concurrently expunged
// by another connection. SEARCH uses the actual selected mailbox state.
type expungedUpdateFetch struct {
	imapserver.Session
	arm           *atomic.Bool
	searches      *atomic.Int64
	searchFailure bool
	outsideSet    bool
}

func (s *expungedUpdateFetch) Fetch(w *imapserver.FetchWriter, set imap.NumSet, opts *imap.FetchOptions) error {
	if uids, ok := set.(imap.UIDSet); ok && opts != nil && len(opts.BodySection) > 0 && s.arm.Load() && uids.Contains(1) {
		m := w.CreateMessage(1)
		m.WriteUID(1)
		m.WriteFlags([]imap.Flag{imap.FlagDeleted})
		if err := m.Close(); err != nil {
			return err
		}
	}
	return s.Session.Fetch(w, set, opts)
}
func (s *expungedUpdateFetch) Search(kind imapserver.NumKind, c *imap.SearchCriteria, o *imap.SearchOptions) (*imap.SearchData, error) {
	if !s.arm.Load() {
		return s.Session.Search(kind, c, o)
	}
	s.searches.Add(1)
	if s.searchFailure {
		return nil, &imap.Error{Type: imap.StatusResponseTypeNo, Text: "injected SEARCH failure"}
	}
	if s.outsideSet {
		return &imap.SearchData{All: imap.UIDSetNum(99)}, nil
	}
	return s.Session.Search(kind, c, o)
}
func TestTailStreamQueuedUpdateForExpungedUIDDoesNotKillReader(t *testing.T) {
	for _, mode := range []string{"resolved", "search-no", "outside-set"} {
		t.Run(mode, func(t *testing.T) {
			var arm atomic.Bool
			var searches atomic.Int64
			cfg, tc := testServerWithSession(t, "tls", true, func(s imapserver.Session) imapserver.Session {
				return &expungedUpdateFetch{Session: s, arm: &arm, searches: &searches, searchFailure: mode == "search-no", outsideSet: mode == "outside-set"}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			reader, err := dial(ctx, cfg, []string{"update-expunge"}, tc)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			cleaner, err := dial(ctx, cfg, []string{"update-expunge"}, tc)
			if err != nil {
				t.Fatal(err)
			}
			defer cleaner.Close()
			if err = reader.Ensure(ctx, "update-expunge"); err != nil {
				t.Fatal(err)
			}
			for _, body := range []string{"deleted", "surviving"} {
				raw, _ := Encode([]byte(body))
				if err = reader.Append(ctx, "update-expunge", raw); err != nil {
					t.Fatal(err)
				}
			}
			rows, tail, err := reader.Recent(ctx, "update-expunge", 2)
			if err != nil || len(rows) != 2 {
				t.Fatal(rows, tail, err)
			}
			if err = cleaner.Delete(ctx, rows[0]); err != nil {
				t.Fatal(err)
			}
			arm.Store(true)
			cur := Cursor{Validity: tail.Validity}
			counts := map[uint32]int{}
			got, next, err := reader.PageTailStream(ctx, "update-expunge", cur, false, 4, func(m Message) error { counts[m.UID]++; return nil })
			if mode != "resolved" {
				if err == nil || next != cur {
					t.Fatal("unproven absence advanced cursor", next, err)
				}
				if mode == "outside-set" && !errors.Is(err, ErrScope) {
					t.Fatal("malicious search escaped scope classification", err)
				}
				return
			}
			if err != nil || len(got) != 1 || got[0].UID != 2 || next.After != 2 || counts[1] != 0 || counts[2] != 1 {
				t.Fatal("expunged update lost valid BODY or killed reader", got, next, counts, err)
			}
			if searches.Load() != 1 || reader.partialRetries.Load() != 0 {
				t.Fatal("absence was not resolved on first tagged FETCH", searches.Load(), reader.partialRetries.Load())
			}
			if _, err = reader.Ping(ctx); err != nil {
				t.Fatal("reader connection did not survive", err)
			}
		})
	}
}
