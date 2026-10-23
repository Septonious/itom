package mailbox

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

type dev8LaggedIndex struct {
	imapserver.Session
	lag *atomic.Bool
}

func (s dev8LaggedIndex) Search(kind imapserver.NumKind, criteria *imap.SearchCriteria, opts *imap.SearchOptions) (*imap.SearchData, error) {
	if kind == imapserver.NumKindUID && s.lag.Load() {
		return &imap.SearchData{All: imap.UIDSet{}}, nil
	}
	return s.Session.Search(kind, criteria, opts)
}

// Cleanup can remove an entire bounded UID window between receive turns.
// A new short message beyond that hole must not wait for the two-second
// periodic SELECT audit, including when IDLE already consumed the EXISTS wake.
func TestDEV8LiveTailFindsShortMessagePastFreshCleanupHole(t *testing.T) {
	for _, indexLag := range []bool{false, true} {
		for _, limit := range []uint32{1, 4, 16} {
			for _, wait := range []bool{false, true} {
				t.Run(fmt.Sprintf("limit%d-idle%t-indexlag%t", limit, wait, indexLag), func(t *testing.T) {
					var lag atomic.Bool
					cfg, tc := testServerWithCaps(t, "tls", imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapUIDPlus: {}, imap.CapIdle: {}}, func(s imapserver.Session) imapserver.Session {
						return dev8LaggedIndex{s, &lag}
					})
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer cancel()
					reader, err := dial(ctx, cfg, []string{"live"}, tc)
					if err != nil {
						t.Fatal(err)
					}
					defer reader.Close()
					writer, err := dial(ctx, cfg, []string{"live"}, tc)
					if err != nil {
						t.Fatal(err)
					}
					defer writer.Close()
					if err = reader.Ensure(ctx, "live"); err != nil {
						t.Fatal(err)
					}
					cur, err := reader.Tail(ctx, "live")
					if err != nil {
						t.Fatal(err)
					}
					raw, _ := Encode([]byte("completed bulk copy"))
					for i := uint32(0); i <= limit; i++ {
						if err = writer.Append(ctx, "live", raw); err != nil {
							t.Fatal(err)
						}
					}
					var all []Message
					var scan Cursor
					for {
						rows, next, e := writer.Page(ctx, "live", scan)
						if e != nil {
							t.Fatal(e)
						}
						all = append(all, rows...)
						if next == scan {
							break
						}
						scan = next
					}
					if err = writer.DeleteObservedMany(ctx, all); err != nil {
						t.Fatal(err)
					}
					// Enable replica index lag only after real cleanup completes.
					// The fixture must not change UID EXPUNGE's own target lookup.
					lag.Store(indexLag)
					short, _ := Encode([]byte("next short response"))
					if err = writer.Append(ctx, "live", short); err != nil {
						t.Fatal(err)
					}
					if wait {
						if _, err = reader.IdleWait(ctx, "live", 100*time.Millisecond); err != nil {
							t.Fatal(err)
						}
					}
					rows, next, err := reader.PageTailLimit(ctx, "live", cur, false, limit)
					if err != nil || len(rows) != 1 || rows[0].UID != limit+2 || string(rows[0].MIME) != string(short) || next.After != limit+2 || next.Validity != cur.Validity {
						t.Fatalf("short response stranded behind expunged UIDs: rows=%d next=%+v err=%v", len(rows), next, err)
					}
				})
			}
		}
	}
}
