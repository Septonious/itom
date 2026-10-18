package tunnel

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
	"itom.local/core/wallet"
	"itom.local/core/wire"
)

// A lost cleanup acknowledgement can replay a descriptor after its BODY has
// been deleted. Registered and Released are different cumulative totals: the
// newer ordinal can be gone while an older one still holds all of its debt.
func TestRC8ReclaimedDescriptorReplayCannotResurrectBodyCache(t *testing.T) {
	a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
	u := servercfg.User{ID: "12345678", Up: "itom-u-up", Down: "itom-u-down"}
	s, err := ledger(t.TempDir(), "data", a, u, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
		t.Fatal(err)
	}
	cipher := cleanupFixtureEpoch(t)
	w, err := wallet.OpenBound(filepath.Join(t.TempDir(), "wallet.db"), s.StoreID(), cipher)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	g, err := s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: w.Session()}, pool.Amount{Bytes: fastLeaseBytes, Messages: fastLeaseMessages})
	if err != nil {
		t.Fatal(err)
	}
	offer, err := s.RemoteOffer(g.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Accept(offer); err != nil {
		t.Fatal(err)
	}
	l := &lane{ctx: context.Background(), side: true, account: a, user: u, data: s, cipher: cipher,
		reports: map[[32]byte]wire.UsageReport{}, pending: map[[32]byte]mailbox.Message{},
		urgentQueue: make(chan queuedControl, 32), maintenanceQueue: make(chan queuedControl, 32), controlQueue: make(chan queuedControl, 32)}
	establishRecoveredServer(t, l)
	mail := &memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}
	if err = mail.Ensure(l.ctx, u.Up); err != nil {
		t.Fatal(err)
	}
	var reports []wire.UsageReport
	process := func(r wire.UsageReport) {
		t.Helper()
		f, err := wire.EncodeControl(0, r)
		if err != nil {
			t.Fatal(err)
		}
		if err = l.process(f); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		r, body, err := w.Spend(offer.Key, u.Up, bytes.Repeat([]byte{byte(i + 1)}, 256), func(b []byte) (uint64, error) {
			raw, err := mailbox.Encode(b)
			return uint64(len(raw)), err
		})
		if err != nil {
			t.Fatal(err)
		}
		process(r)
		reports = append(reports, r)
		raw, err := mailbox.Encode(body)
		if err != nil {
			t.Fatal(err)
		}
		if err = mail.Append(l.ctx, u.Up, raw); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := inventory(l.ctx, mail, u.Up)
	if err != nil {
		t.Fatal(err)
	}
	// Reclaim ordinal 2 first; ordinal 1 must remain live and charged.
	gone, err := reclaim(l.ctx, mail, s, rows, func(a journal.Attempt) bool { return a.Ordinal == 2 })
	if err != nil || len(gone) != 1 {
		t.Fatalf("cleanup: %v %v", gone, err)
	}
	delete(l.reports, reports[1].Attempt.Digest)
	before, err := s.Held(a.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; len(l.reports) < dataDescriptorCacheLimit; i++ {
		// Never evict unrelated live descriptors, even at the hard cache limit.
		l.reports[[32]byte{255, byte(i), byte(i >> 8)}] = wire.UsageReport{}
	}
	l.pending[reports[1].Attempt.Digest] = rows[1]
	process(reports[1])
	if _, ok := l.reports[reports[1].Attempt.Digest]; ok {
		t.Fatal("reclaimed descriptor resurrected")
	}
	if _, ok := l.pending[reports[1].Attempt.Digest]; ok {
		t.Fatal("reclaimed duplicate BODY retained")
	}
	if len(l.reports) != dataDescriptorCacheLimit {
		t.Fatal("unrelated live descriptors evicted")
	}
	process(reports[0])
	if _, ok := l.reports[reports[0].Attempt.Digest]; !ok {
		t.Fatal("live older ordinal discarded")
	}
	if _, err = s.RemoteAttempt(offer.Key, 1); err != nil {
		t.Fatal("live debt lost", err)
	}
	if _, err = s.RemoteAttempt(offer.Key, 2); !errors.Is(err, journal.ErrMissing) {
		t.Fatal("reclaimed row recreated", err)
	}
	after, err := s.Held(a.ID, u.ID)
	if err != nil || before != after {
		t.Fatal("replay changed quota", before, after, err)
	}
	if err = s.Audit(); err != nil {
		t.Fatal(err)
	}
	var acknowledged bool
	for _, q := range []chan queuedControl{l.urgentQueue, l.maintenanceQueue, l.controlQueue} {
		for len(q) > 0 {
			c, err := wire.DecodeControl(l.resolveQuotaControl(<-q).Frame)
			if err != nil {
				t.Fatal(err)
			}
			if status, ok := c.(wire.CleanupStatus); ok && status.Registered.Messages == 2 && status.Released.Messages == 1 {
				acknowledged = true
			}
		}
	}
	if !acknowledged {
		t.Fatal("lost cleanup acknowledgement cannot converge")
	}
}
