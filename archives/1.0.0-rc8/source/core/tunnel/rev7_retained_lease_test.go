package tunnel

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
	"itom.local/core/wallet"
	"itom.local/core/wire"
)

// Field-state capacity must sustain consecutive full-size bulk copies, not
// force a new grant handshake after every single physical BODY. Old unknown
// allocations and the local latency/bulk cushions remain fully charged.
func TestRev7RetainedDebtAllowsConsecutiveBulkBodiesWithoutGrantRoundTrips(t *testing.T) {
	a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
	u := servercfg.User{ID: "12345678", Up: "up", Down: "down"}
	s, err := journal.Open(filepath.Join(t.TempDir(), "data.db"), ledgerConfig(a, u, dataProfile12), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.EnableAfterInventory(a.ID, u.ID, s.Run())
	old, err := s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{99}}, pool.Amount{Bytes: 6 << 20, Messages: 130})
	if err != nil {
		t.Fatal(err)
	}
	l := &lane{ctx: context.Background(), side: true, account: a, user: u, data: s, cipher: cleanupFixtureEpoch(t), stripe: newStripeGroup(wire.ID{1}, 1), issued: map[wire.QuotaKey]bool{}, issuedFast: map[wire.QuotaKey]bool{}, urgentQueue: make(chan queuedControl, 64), controlQueue: make(chan queuedControl, 64), maintenanceQueue: make(chan queuedControl, 64)}
	establishRecoveredServer(t, l)
	if err = l.offer(); err != nil {
		t.Fatal(err)
	}
	w, err := wallet.OpenBound(filepath.Join(t.TempDir(), "wallet.db"), s.StoreID(), l.cipher)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	bulk := 0
	for len(l.urgentQueue) > 0 {
		frame := <-l.urgentQueue
		c, err := wire.DecodeControl(frame.Frame)
		if err != nil {
			t.Fatal(err)
		}
		grant, ok := c.(wire.StorageGrant)
		if !ok {
			continue
		}
		if l.issuedFast[grant.Key] {
			continue
		}
		bulk++
		if err = w.Accept(grant); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			report, _, err := w.Spend(grant.Key, u.Up, bytes.Repeat([]byte{byte(i + 1)}, 330<<10), func(b []byte) (uint64, error) { return mailbox.EncodedSize(len(b)) })
			if err != nil {
				t.Fatalf("usable capacity stranded: bulk=%d body=%d quota=%+v: %v", bulk, i, grant.Limit, err)
			}
			if _, err = s.ApplyRemote(u.ID, s.Run(), report); err != nil {
				t.Fatal(err)
			}
		}
	}
	if bulk != 2 {
		t.Fatal("missing simultaneous bulk leases", bulk)
	}
	got, err := s.GetGrant(old.Ref)
	if err != nil || got != old {
		t.Fatal("old debt changed without proof", got, err)
	}
	for _, fast := range []bool{true, false} {
		g, err := l.tryLocalGrant(dataBatchMIMEHardCap, fast)
		if err != nil {
			t.Fatal("outbound reserve lost", fast, err)
		}
		s.CloseGrant(g.Ref)
	}
	if err = s.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestRev7RetainedDownloadCannotTakeUploadRefillShare(t *testing.T) {
	a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
	u := servercfg.User{ID: "12345678", Up: "up", Down: "down"}
	s, err := journal.Open(filepath.Join(t.TempDir(), "data.db"), ledgerConfig(a, u, dataProfile12), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.EnableAfterInventory(a.ID, u.ID, s.Run())
	old, err := s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{99}}, pool.Amount{Bytes: 6 << 20, Messages: 130})
	if err != nil {
		t.Fatal(err)
	}
	l := &lane{ctx: context.Background(), side: true, account: a, user: u, data: s, cipher: cleanupFixtureEpoch(t), stripe: newStripeGroup(wire.ID{1}, 1), issued: map[wire.QuotaKey]bool{}, issuedFast: map[wire.QuotaKey]bool{}, urgentQueue: make(chan queuedControl, 64), controlQueue: make(chan queuedControl, 64), maintenanceQueue: make(chan queuedControl, 64)}
	establishRecoveredServer(t, l)
	if err = l.offer(); err != nil {
		t.Fatal(err)
	}
	// A fence returns UNUSED permission only; no APPEND has spent these leases.
	for key, fast := range l.issuedFast {
		if !fast {
			if _, err = s.ApplyRemote(u.ID, s.Run(), wire.StorageRelease{Key: key}); err != nil {
				t.Fatal(err)
			}
			l.issued[key] = false
		}
	}
	var local []journal.Grant
	for i := 0; i < 12; i++ {
		g, err := l.tryLocalGrant(dataBatchMIMEHardCap, false)
		if errors.Is(err, journal.ErrLimit) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		local = append(local, g)
	}
	if len(local) != 6 {
		t.Fatal("download borrowed inbound refill capacity", len(local))
	}
	if err = l.offer(); err != nil {
		t.Fatal(err)
	}
	fast, bulk := l.issuedGrantCounts()
	if fast != 1 || bulk != 2 {
		t.Fatal("upload lost replacement leases while download is busy", fast, bulk)
	}
	got, err := s.GetGrant(old.Ref)
	if err != nil || got != old {
		t.Fatal("historical debt changed", got, err)
	}
	if err = s.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestRev7ReducedBulkLeaseCannotImpersonateFastGrant(t *testing.T) {
	a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
	u := servercfg.User{ID: "12345678", Up: "up", Down: "down"}
	s, err := journal.Open(filepath.Join(t.TempDir(), "data.db"), ledgerConfig(a, u, dataProfile12), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.EnableAfterInventory(a.ID, u.ID, s.Run())
	if _, err = s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{99}}, pool.Amount{Bytes: 6 << 20, Messages: 1}); err != nil {
		t.Fatal(err)
	}
	l := &lane{ctx: context.Background(), side: true, account: a, user: u, data: s, cipher: cleanupFixtureEpoch(t), stripe: newStripeGroup(wire.ID{1}, 1), issued: map[wire.QuotaKey]bool{}, issuedFast: map[wire.QuotaKey]bool{}, urgentQueue: make(chan queuedControl, 64), controlQueue: make(chan queuedControl, 64), maintenanceQueue: make(chan queuedControl, 64)}
	if _, err = s.Issue(journal.Scope{Account: a.ID, User: u.ID, Session: l.cipher.Context().Session}, pool.Amount{Bytes: 3 << 20, Messages: 1}); err != nil {
		t.Fatal(err)
	}
	establishRecoveredServer(t, l)
	if err = l.offer(); err != nil {
		t.Fatal(err)
	}
	reduced := 0
	for len(l.urgentQueue) > 0 {
		c, err := wire.DecodeControl((<-l.urgentQueue).Frame)
		if err != nil {
			t.Fatal(err)
		}
		g, ok := c.(wire.StorageGrant)
		if !ok || l.issuedFast[g.Key] {
			continue
		}
		if g.Limit.Bytes == dataBatchMIMEHardCap {
			reduced++
		}
		classifiedFast := g.Limit.Bytes == fastLeaseBytes && g.Limit.Messages == fastLeaseMessages
		if classifiedFast {
			t.Fatal("bulk permission will enter the bounded fast queue", g.Limit)
		}
	}
	if reduced == 0 {
		t.Fatal("one-BODY recovery grant was not exercised")
	}
}

func TestRev7RetainedSmallBodiesKeepBothDirectionalOrdinalHeadrooms(t *testing.T) {
	a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
	u := servercfg.User{ID: "12345678", Up: "up", Down: "down"}
	s, err := journal.Open(filepath.Join(t.TempDir(), "data.db"), ledgerConfig(a, u, dataProfile12), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.EnableAfterInventory(a.ID, u.ID, s.Run())
	old, err := s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{99}}, pool.Amount{Bytes: 6 << 20, Messages: 130})
	if err != nil {
		t.Fatal(err)
	}
	l := &lane{ctx: context.Background(), side: true, account: a, user: u, data: s, cipher: cleanupFixtureEpoch(t), stripe: newStripeGroup(wire.ID{1}, 1), issued: map[wire.QuotaKey]bool{}, issuedFast: map[wire.QuotaKey]bool{}, urgentQueue: make(chan queuedControl, 64), controlQueue: make(chan queuedControl, 64), maintenanceQueue: make(chan queuedControl, 64)}
	establishRecoveredServer(t, l)
	if err = l.offer(); err != nil {
		t.Fatal(err)
	}
	// Real multiplexed snapshots showed fourteen tiny local messages filling
	// the ordinal share while less than half its byte window was occupied.
	for i := 0; i < 20; i++ {
		if _, err = l.tryLocalGrant(64<<10, false); err != nil {
			t.Fatalf("small BODY %d stranded free duplex capacity: %v", i+1, err)
		}
	}
	if _, err = l.tryLocalGrant(dataBatchMIMEHardCap, true); err != nil {
		t.Fatal("local latency reserve lost", err)
	}
	fast, bulk := l.issuedGrantCounts()
	if fast != 1 || bulk != 2 {
		t.Fatal("incoming bulk/latency leases displaced", fast, bulk)
	}
	got, err := s.GetGrant(old.Ref)
	if err != nil || got != old {
		t.Fatal("historical debt changed", got, err)
	}
	if err = s.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestRev7OrderedAdmissionCancelledBeforePhysicalWorkReleasesFlight(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g := newStripeGroup(wire.ID{1}, 1)
	l := &lane{ctx: ctx, ownerCtx: ctx, stripe: g}
	jobs := deliveryJobs(t, 1, 2, 32<<10)
	work := l.AdmitBatch(jobs)
	if g.delivery.bulk != 64<<10 || g.prepared != 0 {
		t.Fatal("unexpected pre-IO admission state", g.delivery.bulk, g.prepared)
	}
	cancel()
	results := work.Write()
	if len(results) != len(jobs) {
		t.Fatal("lost completions", len(results))
	}
	for _, r := range results {
		if !errors.Is(r.Err, context.Canceled) {
			t.Fatal("physical work escaped cancelled preparation", r.Err)
		}
	}
	if g.delivery.bulk != 0 || len(g.delivery.ranges) != 0 || g.prepared != 0 || len(g.preparation) != 0 {
		t.Fatal("unspent logical reservation leaked after cancel")
	}
}
