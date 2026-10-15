package mailbox

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

type delayedVisibleUID struct {
	imapserver.Session
	hidden *atomic.Bool
}

func (s delayedVisibleUID) Fetch(w *imapserver.FetchWriter, set imap.NumSet, opts *imap.FetchOptions) error {
	if ids, ok := set.(imap.UIDSet); ok && s.hidden.Load() && ids.Contains(1) {
		nums, static := ids.Nums()
		if !static {
			return ErrScope
		}
		var visible imap.UIDSet
		for _, uid := range nums {
			if uid != 1 {
				visible.AddNum(uid)
			}
		}
		if len(visible) == 0 {
			return nil
		}
		set = visible
	}
	return s.Session.Fetch(w, set, opts)
}

// Reproduces the provider trace: UID 1 is successfully appended and allocated,
// then UID 2 is visible first. Advancing to 2 must not permanently lose 1.
func TestTailRecoversDelayedVisibilityBehindCursor(t *testing.T) {
	var hidden atomic.Bool
	hidden.Store(true)
	cfg, tc := testServerWithSession(t, "tls", true, func(s imapserver.Session) imapserver.Session {
		return delayedVisibleUID{s, &hidden}
	})
	c, err := dial(context.Background(), cfg, []string{"late"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	if err := c.Ensure(ctx, "late"); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"coordinator ack", "later release"} {
		raw, _ := Encode([]byte(body))
		if err := c.Append(ctx, "late", raw); err != nil {
			t.Fatal(err)
		}
	}
	rows, cur, err := c.PageTail(ctx, "late", Cursor{}, true)
	if err != nil || len(rows) != 1 || rows[0].UID != 2 || cur.After != 2 {
		t.Fatalf("initial rows=%v cursor=%v err=%v", rows, cur, err)
	}
	snapshot := c.TailDiscovery("late")
	backup, err := dial(ctx, cfg, []string{"late", "other"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	if err := backup.RestoreTailDiscovery(TailDiscovery{folder: "foreign"}); err != ErrScope {
		t.Fatal("foreign discovery accepted", err)
	}
	if err := backup.RestoreTailDiscovery(snapshot); err != nil {
		t.Fatal(err)
	}
	if err := backup.Ensure(ctx, "other"); err != nil {
		t.Fatal(err)
	}
	if _, err := backup.Tail(ctx, "other"); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	c = backup
	hidden.Store(false)
	_, fetchBefore, _ := c.ReadCommandCounts()
	var streamed []Message
	rows, next, err := c.PageTailStream(ctx, "late", cur, false, 16, func(m Message) error {
		streamed = append(streamed, m)
		return nil
	})
	if err != nil || len(rows) != 1 || rows[0].UID != 1 || len(streamed) != 1 || streamed[0].UID != 1 || next != cur {
		t.Fatalf("late row lost: rows=%v streamed=%v cursor=%v err=%v", rows, streamed, next, err)
	}
	_, fetchAfter, _ := c.ReadCommandCounts()
	if fetchAfter-fetchBefore != 1 {
		t.Fatal("revisits added a separate provider roundtrip", fetchAfter-fetchBefore)
	}
	rows, next, err = c.PageTail(ctx, "late", next, false)
	if err != nil || len(rows) != 0 || next != cur {
		t.Fatal("recovered row repeated", len(rows), next, err)
	}
}

func TestTailGapBoundsIncarnationAndOverflow(t *testing.T) {
	c := &Client{}
	now := time.Now()
	for uid := uint32(0); uid < tailGapLimit+32; uid += 16 {
		c.trackTailGaps("x", Cursor{7, uid}, Cursor{7, uid + 16}, nil, now)
	}
	if len(c.tailGapState("x", 7).queue) != tailGapLimit {
		t.Fatal("unbounded gap metadata")
	}
	if len(c.tailGapState("x", 8).queue) != 0 {
		t.Fatal("old UIDVALIDITY gaps retained")
	}
	c.trackTailGaps("x", Cursor{8, ^uint32(0) - 2}, Cursor{8, ^uint32(0)}, nil, now)
	if len(c.tailGapState("x", 8).queue) != 2 {
		t.Fatal("UID overflow")
	}
	c.trackTailGaps("x", Cursor{8, 0}, Cursor{8, 100}, nil, now)
	if len(c.tailGapState("x", 8).queue) != 2 {
		t.Fatal("unbounded input range accepted")
	}
}
