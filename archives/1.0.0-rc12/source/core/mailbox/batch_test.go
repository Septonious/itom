package mailbox

import (
	"context"
	"github.com/emersion/go-imap/v2/imapserver"
	"sync/atomic"
	"testing"
)

func TestDeleteManyAmortizesProofAndPreservesOtherUIDs(t *testing.T) {
	for _, plus := range []bool{true, false} {
		t.Run(map[bool]string{true: "uidplus", false: "fallback"}[plus], func(t *testing.T) {
			var calls atomic.Int32
			cfg, tc := testServerWithSession(t, "tls", plus, func(s imapserver.Session) imapserver.Session { return &countingFetch{s, &calls} })
			ctx := context.Background()
			c, e := dial(ctx, cfg, []string{"batch"}, tc)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			if e = c.Ensure(ctx, "batch"); e != nil {
				t.Fatal(e)
			}
			for i := 0; i < 12; i++ {
				raw, _ := Encode([]byte{byte(i + 1)})
				if e = c.Append(ctx, "batch", raw); e != nil {
					t.Fatal(e)
				}
			}
			rows, _, e := c.Page(ctx, "batch", Cursor{})
			if e != nil {
				t.Fatal(e)
			}
			calls.Store(0)
			if e = c.DeleteMany(ctx, rows[:10]); e != nil {
				t.Fatal(e)
			}
			// UIDPLUS exact-set expunge is itself the deletion proof after the
			// pre-delete digest FETCH, so it needs no post-expunge FETCH. The
			// fallback path still verifies absence after ordinary EXPUNGE.
			want := int32(1)
			if !plus {
				want = 3
			}
			if calls.Load() != want {
				t.Fatalf("FETCH calls=%d want=%d", calls.Load(), want)
			}
			remaining, _, e := c.Page(ctx, "batch", Cursor{})
			if e != nil || len(remaining) != 2 {
				t.Fatal(len(remaining), e)
			}
			if remaining[0].UID != rows[10].UID || remaining[1].UID != rows[11].UID {
				t.Fatal("foreign UID removed")
			}
			// Stale content must reject the whole batch before STORE/EXPUNGE.
			forged := remaining[0]
			forged.MIME = []byte("different")
			if e = c.DeleteMany(ctx, []Message{forged, remaining[1]}); e == nil {
				t.Fatal("accepted mismatched body")
			}
		})
	}
}

func TestDeleteObservedManyUsesUIDPlusWithoutSecondBodyFetch(t *testing.T) {
	var calls atomic.Int32
	cfg, tc := testServerWithSession(t, "tls", true, func(s imapserver.Session) imapserver.Session {
		return &countingFetch{s, &calls}
	})
	ctx := context.Background()
	c, err := dial(ctx, cfg, []string{"observed"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = c.Ensure(ctx, "observed"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		raw, encErr := Encode([]byte{byte(i + 1), 0xa5})
		if encErr != nil {
			t.Fatal(encErr)
		}
		if err = c.Append(ctx, "observed", raw); err != nil {
			t.Fatal(err)
		}
	}
	rows, _, err := c.Page(ctx, "observed", Cursor{})
	if err != nil || len(rows) != 3 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	calls.Store(0)
	if err = c.DeleteObservedMany(ctx, rows[:2]); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("observed UIDPLUS delete repeated body FETCH: calls=%d", got)
	}
	remaining, _, err := c.Page(ctx, "observed", Cursor{})
	if err != nil || len(remaining) != 1 || remaining[0].UID != rows[2].UID {
		t.Fatalf("remaining=%v err=%v", remaining, err)
	}
}

func TestDeleteObservedManyFallbackRetainsFullProof(t *testing.T) {
	var calls atomic.Int32
	cfg, tc := testServerWithSession(t, "tls", false, func(s imapserver.Session) imapserver.Session {
		return &countingFetch{s, &calls}
	})
	ctx := context.Background()
	c, err := dial(ctx, cfg, []string{"observed-fallback"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = c.Ensure(ctx, "observed-fallback"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		raw, encErr := Encode([]byte{byte(i + 1)})
		if encErr != nil {
			t.Fatal(encErr)
		}
		if err = c.Append(ctx, "observed-fallback", raw); err != nil {
			t.Fatal(err)
		}
	}
	rows, _, err := c.Page(ctx, "observed-fallback", Cursor{})
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	calls.Store(0)
	if err = c.DeleteObservedMany(ctx, rows[:1]); err != nil {
		t.Fatal(err)
	}
	// No UIDPLUS means the optimized API delegates to DeleteMany: one body
	// verification FETCH, one deleted-flag safety scan and one absence FETCH.
	if got := calls.Load(); got != 3 {
		t.Fatalf("fallback FETCH calls=%d want=3", got)
	}
}

func TestDeleteObservedManyRejectsChangedUIDValidity(t *testing.T) {
	cfg, tc := testServer(t, "tls", true)
	ctx := context.Background()
	c, err := dial(ctx, cfg, []string{"observed-validity"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = c.Ensure(ctx, "observed-validity"); err != nil {
		t.Fatal(err)
	}
	raw, _ := Encode([]byte("body"))
	if err = c.Append(ctx, "observed-validity", raw); err != nil {
		t.Fatal(err)
	}
	rows, _, err := c.Page(ctx, "observed-validity", Cursor{})
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	stale := rows[0]
	stale.Validity++
	if err = c.DeleteObservedMany(ctx, []Message{stale}); err != ErrChanged {
		t.Fatalf("changed UIDVALIDITY err=%v", err)
	}
	remaining, _, err := c.Page(ctx, "observed-validity", Cursor{})
	if err != nil || len(remaining) != 1 {
		t.Fatalf("stale delete removed message: rows=%d err=%v", len(remaining), err)
	}
}
