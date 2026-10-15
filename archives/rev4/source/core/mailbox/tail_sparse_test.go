package mailbox

import (
	"bytes"
	"context"
	"sync/atomic"
	"testing"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

type sparseReplicaSession struct {
	imapserver.Session
	hidden       *atomic.Bool
	searchHidden bool
	bodyHidden   bool
	searchCalls  *atomic.Int32
}

func (s sparseReplicaSession) Search(kind imapserver.NumKind, criteria *imap.SearchCriteria, opts *imap.SearchOptions) (*imap.SearchData, error) {
	s.searchCalls.Add(1)
	d, err := s.Session.Search(kind, criteria, opts)
	if err != nil || !s.searchHidden || !s.hidden.Load() || kind != imapserver.NumKindUID {
		return d, err
	}
	ids, ok := d.All.(imap.UIDSet)
	if !ok {
		return d, err
	}
	nums, ok := ids.Nums()
	if !ok {
		return d, err
	}
	var visible imap.UIDSet
	for _, id := range nums {
		if id != 65 {
			visible.AddNum(id)
		}
	}
	d.All = visible
	return d, nil
}

func (s sparseReplicaSession) Fetch(w *imapserver.FetchWriter, set imap.NumSet, opts *imap.FetchOptions) error {
	if ids, ok := set.(imap.UIDSet); ok && s.bodyHidden && s.hidden.Load() && ids.Contains(65) {
		nums, static := ids.Nums()
		if !static {
			return ErrScope
		}
		var visible imap.UIDSet
		for _, id := range nums {
			if id != 65 {
				visible.AddNum(id)
			}
		}
		if len(visible) == 0 {
			return nil
		}
		set = visible
	}
	return s.Session.Fetch(w, set, opts)
}

func TestSparseTailRecoversReplicaGapAfterSocketHandoff(t *testing.T) {
	for _, hideSearch := range []bool{false, true} {
		t.Run(map[bool]string{false: "body_lag", true: "index_and_body_lag"}[hideSearch], func(t *testing.T) {
			var hidden atomic.Bool
			var searches atomic.Int32
			cfg, tc := testServerWithSession(t, "tls", true, func(s imapserver.Session) imapserver.Session {
				return sparseReplicaSession{s, &hidden, hideSearch, true, &searches}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c, err := dial(ctx, cfg, []string{"sparse"}, tc)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { c.Close() }()
			if err = c.Ensure(ctx, "sparse"); err != nil {
				t.Fatal(err)
			}
			raw, _ := Encode([]byte("previous session"))
			for i := 0; i < 64; i++ {
				if err = c.Append(ctx, "sparse", raw); err != nil {
					t.Fatal(err)
				}
			}
			cur := Cursor{}
			for i := 0; i < 4; i++ {
				rows, next, err := c.Page(ctx, "sparse", cur)
				if err != nil {
					t.Fatal(err)
				}
				if err = c.DeleteMany(ctx, rows); err != nil {
					t.Fatal(err)
				}
				cur = next
			}
			want, _ := Encode(bytes.Repeat([]byte{0xb7}, 32768))
			for i := 0; i < 2; i++ {
				if err = c.Append(ctx, "sparse", want); err != nil {
					t.Fatal(err)
				}
			}
			hidden.Store(true)
			rows, cur, err := c.PageTailLimit(ctx, "sparse", Cursor{}, true, 4)
			if err != nil || len(rows) != 1 || rows[0].UID != 66 || cur.After != 66 {
				t.Fatalf("sparse discovery rows=%d cur=%+v err=%v", len(rows), cur, err)
			}
			snapshot := c.TailDiscovery("sparse")
			backup, err := dial(ctx, cfg, []string{"sparse"}, tc)
			if err != nil {
				t.Fatal(err)
			}
			if err = backup.RestoreTailDiscovery(snapshot); err != nil {
				t.Fatal(err)
			}
			c.Close()
			c = backup
			hidden.Store(false)
			var delivered []Message
			rows, next, err := c.PageTailStream(ctx, "sparse", cur, false, 4, func(m Message) error { delivered = append(delivered, m); return nil })
			if err != nil || next != cur || len(rows) != 1 || rows[0].UID != 65 || !bytes.Equal(rows[0].MIME, want) || len(delivered) != 1 {
				t.Fatalf("replica BODY lost after handoff rows=%d delivered=%d next=%+v err=%v", len(rows), len(delivered), next, err)
			}
			if searches.Load() < 2 {
				t.Fatal("sparse index path was not exercised")
			}
		})
	}
}

func TestSparseTailIntervalsStayBoundedAndRetainOriginalExpiry(t *testing.T) {
	c := &Client{folders: map[string]bool{"sparse": true}}
	now := time.Now()
	c.trackSparseTail("sparse", 7, 1, ^uint32(0), []Message{{UID: 70000}}, now)
	snap := c.TailDiscovery("sparse")
	if len(snap.state.ranges) != 2 {
		t.Fatal("UID history expanded into individual holes")
	}
	copy := &Client{folders: map[string]bool{"sparse": true}}
	if err := copy.RestoreTailDiscovery(snap); err != nil {
		t.Fatal(err)
	}
	if _, ok := copy.sparseTailRequest("sparse", 7, now.Add(tailGapLifetime)); ok {
		t.Fatal("handoff renewed expired discovery")
	}
	if _, ok := c.sparseTailRequest("sparse", 8, now); ok {
		t.Fatal("old incarnation discovery reused")
	}
}

func sparseHistoryReplica(t *testing.T, hideSearch, hideBody bool) (context.Context, *Client, *atomic.Bool, []byte) {
	t.Helper()
	hidden := new(atomic.Bool)
	searches := new(atomic.Int32)
	cfg, tc := testServerWithSession(t, "tls", true, func(s imapserver.Session) imapserver.Session {
		return sparseReplicaSession{s, hidden, hideSearch, hideBody, searches}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	c, err := dial(ctx, cfg, []string{"sparse"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if err = c.Ensure(ctx, "sparse"); err != nil {
		t.Fatal(err)
	}
	raw, _ := Encode([]byte("historical allocation"))
	for i := 0; i < 64; i++ {
		if err = c.Append(ctx, "sparse", raw); err != nil {
			t.Fatal(err)
		}
	}
	cur := Cursor{}
	for i := 0; i < 4; i++ {
		rows, next, err := c.Page(ctx, "sparse", cur)
		if err != nil {
			t.Fatal(err)
		}
		if err = c.DeleteMany(ctx, rows); err != nil {
			t.Fatal(err)
		}
		cur = next
	}
	want, _ := Encode(bytes.Repeat([]byte{0xa5}, 32768))
	return ctx, c, hidden, want
}

// A replica's UID SEARCH index may lag while an exact UID BODY fetch already
// works. SEARCH absence alone must not strand the newest allocated DATA.
func TestSparseTailSearchLagCannotHideFetchableBODY(t *testing.T) {
	ctx, c, hidden, want := sparseHistoryReplica(t, true, false)
	for i := 0; i < 2; i++ {
		if err := c.Append(ctx, "sparse", want); err != nil {
			t.Fatal(err)
		}
	}
	hidden.Store(true)
	rows, cur, err := c.PageTailLimit(ctx, "sparse", Cursor{}, true, 4)
	seen := map[uint32]bool{}
	for _, row := range rows {
		if !bytes.Equal(row.MIME, want) {
			t.Fatal("BODY integrity lost")
		}
		seen[row.UID] = true
	}
	if err != nil || cur.After != 66 || !seen[65] || !seen[66] {
		t.Fatalf("SEARCH index lag stranded an exact-fetchable BODY: seen=%v cur=%+v err=%v", seen, cur, err)
	}
}

// A continuous forward backlog cannot suppress old sparse visibility revisits.
func TestSparseTailRevisitsCannotStarveBehindForwardBacklog(t *testing.T) {
	ctx, c, hidden, want := sparseHistoryReplica(t, false, true)
	for i := 0; i < 50; i++ {
		if err := c.Append(ctx, "sparse", want); err != nil {
			t.Fatal(err)
		}
	}
	hidden.Store(true)
	rows, cur, err := c.PageTailLimit(ctx, "sparse", Cursor{}, true, 4)
	if err != nil || cur.After != 68 || len(rows) != 3 {
		t.Fatal("initial sparse gap", len(rows), cur, err)
	}
	hidden.Store(false)
	for attempt := 0; attempt < 4; attempt++ {
		rows, cur, err = c.PageTailLimit(ctx, "sparse", cur, false, 4)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.UID == 65 {
				if !bytes.Equal(row.MIME, want) {
					t.Fatal("BODY integrity lost")
				}
				return
			}
		}
	}
	t.Fatal("forward backlog starved an already fetchable sparse BODY", cur)
}

type sparseSearchFault struct {
	imapserver.Session
	oversized bool
}

func (s sparseSearchFault) Select(folder string, opts *imap.SelectOptions) (*imap.SelectData, error) {
	d, err := s.Session.Select(folder, opts)
	if err == nil {
		d.UIDNext = 6001
	}
	return d, err
}
func (s sparseSearchFault) Search(imapserver.NumKind, *imap.SearchCriteria, *imap.SearchOptions) (*imap.SearchData, error) {
	if s.oversized {
		return &imap.SearchData{All: imap.UIDSet{{Start: 1, Stop: 5000}}}, nil
	}
	return &imap.SearchData{All: imap.UIDSetNum(6002)}, nil
}

func TestSparseTailRejectsForeignAndOversizedSearchBeforeBODYDelivery(t *testing.T) {
	for _, oversized := range []bool{false, true} {
		t.Run(map[bool]string{false: "foreign_uid", true: "oversized_uid_set"}[oversized], func(t *testing.T) {
			cfg, tc := testServerWithSession(t, "tls", true, func(s imapserver.Session) imapserver.Session { return sparseSearchFault{s, oversized} })
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			c, err := dial(ctx, cfg, []string{"sparse"}, tc)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if err = c.Ensure(ctx, "sparse"); err != nil {
				t.Fatal(err)
			}
			called := false
			rows, cur, err := c.PageTailStream(ctx, "sparse", Cursor{}, true, 4, func(Message) error { called = true; return nil })
			_, fetches, _ := c.ReadCommandCounts()
			if err != ErrScope || called || len(rows) != 0 || cur != (Cursor{}) || fetches != 0 || c.Ready() {
				t.Fatalf("hostile SEARCH advanced/emitted: rows=%d cur=%+v callback=%t fetches=%d ready=%t err=%v", len(rows), cur, called, fetches, c.Ready(), err)
			}
		})
	}
}
