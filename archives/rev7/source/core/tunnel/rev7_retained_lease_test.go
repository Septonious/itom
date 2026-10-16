package tunnel

import (
	"bytes"
	"context"
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
		for i := 0; i < 3; i++ {
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
