package tunnel

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
	"itom.local/core/wallet"
	"itom.local/core/wire"
)

func rc13RetainedLane(t *testing.T, debt pool.Amount) (*lane, *journal.Store, journal.Grant) {
	t.Helper()
	a := servercfg.Account{ID: "a01"}
	a.Mail.Host = "mail.example"
	u := servercfg.User{ID: "12345678", Up: "up", Down: "down"}
	s, err := journal.Open(filepath.Join(t.TempDir(), "data.db"), ledgerConfig(a, u, dataProfile12), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
		t.Fatal(err)
	}
	var old journal.Grant
	if debt.Bytes > 0 || debt.Messages > 0 {
		old, err = s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{99}}, debt)
		if err != nil {
			t.Fatal(err)
		}
	}
	l := &lane{ctx: context.Background(), side: true, account: a, user: u, data: s, cipher: cleanupFixtureEpoch(t), stripe: newStripeGroup(wire.ID{1}, 1), issued: map[wire.QuotaKey]bool{}, issuedFast: map[wire.QuotaKey]bool{}, urgentQueue: make(chan queuedControl, 128), controlQueue: make(chan queuedControl, 128), maintenanceQueue: make(chan queuedControl, 128)}
	establishRecoveredServer(t, l)
	return l, s, old
}

// Exact pre-offer retained charges from the three RC12 accounts that never
// reached ready. All old debt stays charged; both live directions need room.
func TestRC13FieldBytePressureBootstrap(t *testing.T) {
	for _, debt := range []pool.Amount{{Bytes: 10915159, Messages: 154}, {Bytes: 10832733, Messages: 161}, {Bytes: 10813503, Messages: 149}} {
		t.Run(fmt.Sprint(debt.Bytes), func(t *testing.T) {
			l, s, old := rc13RetainedLane(t, debt)
			if err := l.offer(); err != nil {
				t.Fatal(err)
			}
			fast, bulk := l.issuedGrantCounts()
			if !l.ready.Load() || fast != 1 || bulk < 1 {
				t.Fatalf("field account stranded: ready=%t fast=%d bulk=%d debt=%+v", l.ready.Load(), fast, bulk, debt)
			}
			g, err := l.tryLocalGrant(dataBatchMIMEHardCap, false)
			if err != nil {
				t.Fatal("no full local bulk BODY headroom", err)
			}
			if err = s.CloseGrant(g.Ref); err != nil {
				t.Fatal(err)
			}
			g, err = l.tryLocalGrant(128<<10, true)
			if err != nil {
				t.Fatal("no bounded local fast headroom", err)
			}
			if err = s.CloseGrant(g.Ref); err != nil {
				t.Fatal(err)
			}
			got, err := s.GetGrant(old.Ref)
			if err != nil || got != old {
				t.Fatal("historical debt changed", err)
			}
			if err = s.Audit(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A single-ordinal recovery bulk lease is common at 175+/192 retained slots.
// Reported exhaustion must pipeline the next permission before a separate fence,
// while retaining the original open grant and its full physical charge.
func TestRC13SpentBulkRefillBeforeFence(t *testing.T) {
	l, s, _ := rc13RetainedLane(t, pool.Amount{})
	if err := l.offer(); err != nil {
		t.Fatal(err)
	}
	var g wire.StorageGrant
	for len(l.urgentQueue) > 0 {
		c, err := wire.DecodeControl((<-l.urgentQueue).Frame)
		if err != nil {
			t.Fatal(err)
		}
		if x, ok := c.(wire.StorageGrant); ok && !l.issuedFast[x.Key] {
			g = x
		}
	}
	if g.Key.Grant == 0 {
		t.Fatal("no initial bulk lease")
	}
	w, err := wallet.OpenBound(filepath.Join(t.TempDir(), "wallet.db"), s.StoreID(), l.cipher)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err = w.Accept(g); err != nil {
		t.Fatal(err)
	}
	var status wire.CleanupStatus
	for i := uint64(0); i < g.Limit.Messages; i++ {
		r, _, err := w.Spend(g.Key, l.user.Up, bytes.Repeat([]byte{byte(i)}, 256), canonicalDataSize)
		if err != nil {
			t.Fatal(err)
		}
		status, err = s.ApplyRemote(l.user.ID, s.Run(), r)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = l.publishCleanup(status); err != nil {
		t.Fatal(err)
	}
	if l.issued[g.Key] {
		t.Fatal("spent bulk grant still counted usable before fence")
	}
	_, bulk := l.issuedGrantCounts()
	if bulk != remoteBulkLeases {
		t.Fatal("replacement not pipelined", bulk)
	}
	old, err := s.GetGrant(journal.GrantRef{Store: g.Key.Store, ID: g.Key.Grant, Scope: journal.Scope{Account: g.Key.Account, User: g.Key.User, Session: g.Key.Session}})
	if err != nil || old.Closed || old.Released != (pool.Amount{}) {
		t.Fatal("unproved debt refunded", old, err)
	}
	count := len(l.issued)
	if err = l.publishCleanup(status); err != nil {
		t.Fatal(err)
	}
	if len(l.issued) != count {
		t.Fatal("duplicate status issued another grant")
	}
	if err = s.Audit(); err != nil {
		t.Fatal(err)
	}
}
