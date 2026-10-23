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

// Folder switches on a shared control/cleanup socket must keep the same
// speculative-read fence as periodic SELECT renewal on a dedicated reader.
func TestDEV7SharedTailSwitchFencesSelectionBeforeDelivery(t *testing.T) {
	for _, mode := range []int32{0, 1, 2} {
		t.Run(map[int32]string{0: "unchanged", 1: "changed", 2: "failed"}[mode], func(t *testing.T) {
			var state atomic.Int32
			cfg, tc := testServerWithSession(t, "tls", true, func(s imapserver.Session) imapserver.Session { return &changedRenewal{s, &state} })
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			c, err := dial(ctx, cfg, []string{"live", "cleanup"}, tc)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for _, folder := range []string{"live", "cleanup"} {
				if err = c.Ensure(ctx, folder); err != nil {
					t.Fatal(err)
				}
			}
			cur, err := c.Tail(ctx, "live")
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := Encode([]byte("scoped row"))
			if err = c.Append(ctx, "live", raw); err != nil {
				t.Fatal(err)
			}
			if _, err = c.Tail(ctx, "cleanup"); err != nil {
				t.Fatal(err)
			}
			state.Store(mode)
			var delivered []Message
			rows, next, err := c.PageTailStream(ctx, "live", cur, false, 4, func(m Message) error { delivered = append(delivered, m); return nil })
			if mode == 2 {
				var status *imap.Error
				if !errors.As(err, &status) || len(rows) != 0 || len(delivered) != 0 || next != cur {
					t.Fatal("failed selection exposed rows", rows, next, delivered, err)
				}
				return
			}
			wantValidity := cur.Validity + uint32(mode)
			if err != nil || len(rows) != 1 || len(delivered) != 1 || next.Validity != wantValidity || delivered[0].Validity != wantValidity || string(delivered[0].MIME) != string(raw) {
				t.Fatal("folder/incarnation scope lost", rows, next, delivered, err)
			}
		})
	}
}

type dev7DeleteFailure struct {
	imapserver.Session
	mode     int
	expunges *atomic.Int32
}

func (s dev7DeleteFailure) Store(w *imapserver.FetchWriter, n imap.NumSet, f *imap.StoreFlags, o *imap.StoreOptions) error {
	if s.mode == 1 {
		return &imap.Error{Type: imap.StatusResponseTypeNo, Text: "test store failure"}
	}
	return s.Session.Store(w, n, f, o)
}
func (s dev7DeleteFailure) Expunge(w *imapserver.ExpungeWriter, n *imap.UIDSet) error {
	s.expunges.Add(1)
	if s.mode == 2 {
		return &imap.Error{Type: imap.StatusResponseTypeNo, Text: "test expunge failure"}
	}
	return s.Session.Expunge(w, n)
}
func TestDEV7ObservedDeleteCannotHideEitherCommandFailure(t *testing.T) {
	for _, mode := range []int{1, 2} {
		t.Run(map[int]string{1: "store", 2: "expunge"}[mode], func(t *testing.T) {
			var expunges atomic.Int32
			cfg, tc := testServerWithSession(t, "tls", true, func(s imapserver.Session) imapserver.Session { return dev7DeleteFailure{s, mode, &expunges} })
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			c, err := dial(ctx, cfg, []string{"observed"}, tc)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if err = c.Ensure(ctx, "observed"); err != nil {
				t.Fatal(err)
			}
			raw, _ := Encode([]byte("unchanged debt on failure"))
			if err = c.Append(ctx, "observed", raw); err != nil {
				t.Fatal(err)
			}
			rows, _, err := c.Page(ctx, "observed", Cursor{})
			if err != nil || len(rows) != 1 {
				t.Fatal(err)
			}
			if err = c.DeleteObservedMany(ctx, rows); err == nil {
				t.Fatal("failed proof reported success")
			}
			if expunges.Load() != 1 {
				t.Fatal("command outcome was not joined", expunges.Load())
			}
		})
	}
}

func TestDEV7ExactDiscoveryNeverReturnsChangedOrFailedSelectionRows(t *testing.T) {
	for _, mode := range []int32{0, 1, 2} {
		t.Run(map[int32]string{0: "unchanged", 1: "changed", 2: "failed"}[mode], func(t *testing.T) {
			var state atomic.Int32
			cfg, tc := testServerWithSession(t, "tls", true, func(s imapserver.Session) imapserver.Session { return &changedRenewal{s, &state} })
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			c, err := dial(ctx, cfg, []string{"exact", "other"}, tc)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for _, folder := range []string{"exact", "other"} {
				if err = c.Ensure(ctx, folder); err != nil {
					t.Fatal(err)
				}
			}
			raw, _ := Encode([]byte("exact body"))
			if err = c.Append(ctx, "exact", raw); err != nil {
				t.Fatal(err)
			}
			rows, _, err := c.Page(ctx, "exact", Cursor{})
			if err != nil || len(rows) != 1 {
				t.Fatal(err)
			}
			observed := rows[0]
			if _, err = c.Tail(ctx, "other"); err != nil {
				t.Fatal(err)
			}
			state.Store(mode)
			found, err := c.FetchExact(ctx, "exact", observed.Validity, []uint32{observed.UID})
			if mode != 0 {
				if err == nil || len(found) != 0 {
					t.Fatal("unsafe exact discovery", found, err)
				}
				if mode == 1 && !errors.Is(err, ErrChanged) {
					t.Fatal(err)
				}
			} else if err != nil || len(found) != 1 || found[0].Validity != observed.Validity || string(found[0].MIME) != string(raw) {
				t.Fatal("exact row lost", found, err)
			}
		})
	}
}

func TestDEV7PresenceCannotProveAbsenceAcrossFailedSelection(t *testing.T) {
	for _, mode := range []int32{1, 2} {
		t.Run(map[int32]string{1: "changed", 2: "failed"}[mode], func(t *testing.T) {
			var state atomic.Int32
			cfg, tc := testServerWithSession(t, "tls", true, func(s imapserver.Session) imapserver.Session { return &changedRenewal{s, &state} })
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			c, err := dial(ctx, cfg, []string{"presence"}, tc)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if err = c.Ensure(ctx, "presence"); err != nil {
				t.Fatal(err)
			}
			cur, err := c.Tail(ctx, "presence")
			if err != nil {
				t.Fatal(err)
			}
			state.Store(mode)
			found, err := c.Present(ctx, "presence", cur.Validity, []uint32{1})
			if err == nil || len(found) != 0 {
				t.Fatal("failed or changed selection became absence proof", found, err)
			}
			if mode == 1 && !errors.Is(err, ErrChanged) {
				t.Fatal(err)
			}
		})
	}
}
