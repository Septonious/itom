package mailbox

import (
	"context"
	"fmt"
	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type countingFetch struct {
	imapserver.Session
	calls *atomic.Int32
}

func (s *countingFetch) Fetch(w *imapserver.FetchWriter, set imap.NumSet, opts *imap.FetchOptions) error {
	s.calls.Add(1)
	return s.Session.Fetch(w, set, opts)
}
func TestPageUsesSingleFetchForSixteenUIDsAndHandlesHoles(t *testing.T) {
	var count atomic.Int32
	cfg, tc := testServerWithSession(t, "tls", true, func(s imapserver.Session) imapserver.Session { return &countingFetch{s, &count} })
	ctx := context.Background()
	c, e := dial(ctx, cfg, []string{"page"}, tc)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	if e = c.Ensure(ctx, "page"); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 18; i++ {
		raw, _ := Encode([]byte(fmt.Sprint(i)))
		if e = c.Append(ctx, "page", raw); e != nil {
			t.Fatal(e)
		}
	}
	count.Store(0)
	rows, next, e := c.Page(ctx, "page", Cursor{})
	if e != nil || len(rows) != 16 || next.After != 16 || count.Load() != 1 {
		t.Fatal(len(rows), next, count.Load(), e)
	}
	removed := rows[4]
	if e = c.Delete(ctx, removed); e != nil {
		t.Fatal(e)
	}
	count.Store(0)
	rows, again, e := c.Page(ctx, "page", Cursor{})
	if e != nil || len(rows) != 16 || again.After != 17 || again.Validity != next.Validity || count.Load() != 1 {
		t.Fatal(len(rows), again, count.Load(), e)
	}
	for _, row := range rows {
		if row.UID == removed.UID {
			t.Fatal("expunged UID returned")
		}
	}
	count.Store(0)
	rows, end, e := c.Page(ctx, "page", next)
	if e != nil || len(rows) != 2 || end.After != 18 || count.Load() != 1 {
		t.Fatal(len(rows), end, count.Load(), e)
	}
	count.Store(0)
	rows, last, e := c.Page(ctx, "page", end)
	if e != nil || len(rows) != 0 || last != end || count.Load() != 0 {
		t.Fatal(len(rows), last, count.Load(), e)
	}
}

// A hostile response must not advance the cursor, including when SELECT
// discovers a different UIDVALIDITY before the malformed FETCH arrives.
type malformedFetch struct{ imapserver.Session }

func (s malformedFetch) Fetch(w *imapserver.FetchWriter, set imap.NumSet, opts *imap.FetchOptions) error {
	m := w.CreateMessage(1)
	m.WriteUID(1)
	m.WriteRFC822Size(2)
	body := m.WriteBodySection(opts.BodySection[0], 1)
	if _, e := body.Write([]byte("x")); e != nil {
		return e
	}
	if e := body.Close(); e != nil {
		return e
	}
	return m.Close()
}
func TestPageRejectsSizeMismatchAndPreservesOriginalCursor(t *testing.T) {
	cfg, tc := testServerWithSession(t, "tls", true, func(s imapserver.Session) imapserver.Session { return malformedFetch{s} })
	ctx := context.Background()
	c, e := dial(ctx, cfg, []string{"page"}, tc)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	if e = c.Ensure(ctx, "page"); e != nil {
		t.Fatal(e)
	}
	raw, _ := Encode([]byte("payload"))
	if e = c.Append(ctx, "page", raw); e != nil {
		t.Fatal(e)
	}
	original := Cursor{Validity: 424242, After: 13}
	rows, next, e := c.Page(ctx, "page", original)
	if e == nil || len(rows) != 0 || next != original {
		t.Fatal("malformed FETCH changed cursor", rows, next, e)
	}
}

type countingTail struct {
	imapserver.Session
	selects *atomic.Int32
	fetches *atomic.Int32
}

func (s *countingTail) Select(name string, opts *imap.SelectOptions) (*imap.SelectData, error) {
	s.selects.Add(1)
	return s.Session.Select(name, opts)
}
func (s *countingTail) Fetch(w *imapserver.FetchWriter, set imap.NumSet, opts *imap.FetchOptions) error {
	s.fetches.Add(1)
	return s.Session.Fetch(w, set, opts)
}

func TestPageTailSkipsRepeatedSelectBetweenValidityChecks(t *testing.T) {
	var selects, fetches atomic.Int32
	cfg, tc := testServerWithSession(t, "tls", true, func(s imapserver.Session) imapserver.Session {
		return &countingTail{Session: s, selects: &selects, fetches: &fetches}
	})
	ctx := context.Background()
	c, err := dial(ctx, cfg, []string{"page"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = c.Ensure(ctx, "page"); err != nil {
		t.Fatal(err)
	}
	// Ignore setup SELECTs performed by Ensure.
	selects.Store(0)
	fetches.Store(0)
	for i := 0; i < 3; i++ {
		raw, _ := Encode([]byte(fmt.Sprint("tail-", i)))
		if err = c.Append(ctx, "page", raw); err != nil {
			t.Fatal(err)
		}
	}
	rows, cur, err := c.PageTail(ctx, "page", Cursor{}, true)
	if err != nil || len(rows) != 3 || cur.Validity == 0 || cur.After != 3 || selects.Load() != 1 || fetches.Load() != 1 {
		t.Fatalf("initial tail page rows=%d cursor=%+v select=%d fetch=%d err=%v", len(rows), cur, selects.Load(), fetches.Load(), err)
	}
	raw, _ := Encode([]byte("tail-next"))
	if err = c.Append(ctx, "page", raw); err != nil {
		t.Fatal(err)
	}
	rows, next, err := c.PageTail(ctx, "page", cur, false)
	if err != nil || len(rows) != 1 || next.After != 4 || next.Validity != cur.Validity || selects.Load() != 1 || fetches.Load() != 2 {
		t.Fatalf("fast tail page rows=%d cursor=%+v select=%d fetch=%d err=%v", len(rows), next, selects.Load(), fetches.Load(), err)
	}
	rows, same, err := c.PageTail(ctx, "page", next, false)
	if err != nil || len(rows) != 0 || same != next || selects.Load() != 1 || fetches.Load() != 3 {
		t.Fatalf("empty tail poll rows=%d cursor=%+v select=%d fetch=%d err=%v", len(rows), same, selects.Load(), fetches.Load(), err)
	}
	rows, verified, err := c.PageTail(ctx, "page", next, true)
	if err != nil || len(rows) != 0 || verified != next || selects.Load() != 2 || fetches.Load() != 4 {
		t.Fatalf("validity refresh rows=%d cursor=%+v select=%d fetch=%d err=%v", len(rows), verified, selects.Load(), fetches.Load(), err)
	}
}

func TestRecentReturnsOnlyBoundedMailboxTail(t *testing.T) {
	cfg, tc := testServerWithSession(t, "tls", true, nil)
	ctx := context.Background()
	c, err := dial(ctx, cfg, []string{"page"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = c.Ensure(ctx, "page"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		raw, _ := Encode([]byte(fmt.Sprintf("recent-%02d", i)))
		if err = c.Append(ctx, "page", raw); err != nil {
			t.Fatal(err)
		}
	}
	rows, tail, err := c.Recent(ctx, "page", 8)
	if err != nil || tail.Validity == 0 || tail.After != 40 || len(rows) != 8 {
		t.Fatalf("recent rows=%d tail=%+v err=%v", len(rows), tail, err)
	}
	if rows[0].UID != 33 || rows[len(rows)-1].UID != 40 {
		t.Fatalf("unexpected recent UID range: %d..%d", rows[0].UID, rows[len(rows)-1].UID)
	}
}

// Block after capturing SELECT's UIDNEXT, then append through another session.
// Periodic verification must include that row without waiting for a second FETCH.
type snapshotSelect struct {
	imapserver.Session
	armed            *atomic.Bool
	entered, release chan struct{}
}

func (s *snapshotSelect) Select(f string, opts *imap.SelectOptions) (*imap.SelectData, error) {
	data, err := s.Session.Select(f, opts)
	if err == nil && s.armed.Swap(false) {
		close(s.entered)
		<-s.release
	}
	return data, err
}
func TestVerifiedTailReadsMailAllocatedAfterSelectSnapshot(t *testing.T) {
	for _, initial := range []int{0, 1} {
		t.Run(fmt.Sprint(initial), func(t *testing.T) {
			var armed atomic.Bool
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			cfg, tc := testServerWithSession(t, "tls", true, func(s imapserver.Session) imapserver.Session { return &snapshotSelect{s, &armed, entered, release} })
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			reader, err := dial(ctx, cfg, []string{"tail-race"}, tc)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			writer, err := dial(ctx, cfg, []string{"tail-race"}, tc)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			if err = writer.Ensure(ctx, "tail-race"); err != nil {
				t.Fatal(err)
			}
			raw, _ := Encode([]byte("old"))
			if initial != 0 {
				if err = writer.Append(ctx, "tail-race", raw); err != nil {
					t.Fatal(err)
				}
			}
			armed.Store(true)
			type result struct {
				rows []Message
				cur  Cursor
				err  error
			}
			done := make(chan result, 1)
			go func() {
				rows, cur, err := reader.PageTailLimit(ctx, "tail-race", Cursor{}, true, 4)
				done <- result{rows, cur, err}
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("SELECT did not reach snapshot barrier")
			}
			raw, _ = Encode([]byte("late"))
			if err = writer.Append(ctx, "tail-race", raw); err != nil {
				t.Fatal(err)
			}
			once.Do(func() { close(release) })
			got := <-done
			if got.err != nil || len(got.rows) != initial+1 || got.cur.After != uint32(initial+1) {
				t.Fatal("late row required another poll", got)
			}
			rows, next, err := reader.PageTailLimit(ctx, "tail-race", got.cur, false, 4)
			if err != nil || len(rows) != 0 || next != got.cur {
				t.Fatal("tail skipped unallocated future UIDs", rows, next, err)
			}
		})
	}
}
