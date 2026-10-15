package tunnel

import (
	"bytes"
	"context"
	"testing"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"itom.local/core/wallet"
)

// Historical physical descriptors can disagree with canonical MIME charge.
// They must never turn startup recovery into an endless reconnect loop, and
// must never permit deleting the unproven UID or refunding its durable debt.
func TestField75MixedLegacyScopeRecoveryPreservesDebtAndHealthyUID(t *testing.T) {
	ctx := context.Background()
	a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
	u := servercfg.User{ID: "12345678", Up: "itom-u-up", Down: "itom-u-down"}
	s, err := ledger(t.TempDir(), "data", a, u, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
		t.Fatal(err)
	}
	w, err := wallet.Open(t.TempDir()+"/wallet.db", u.ID, s.StoreID())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	g, err := s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: w.Session()}, dataProfile12)
	if err != nil {
		t.Fatal(err)
	}
	offer, err := s.RemoteOffer(g.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Accept(offer); err != nil {
		t.Fatal(err)
	}
	m := &memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}
	if err := m.Ensure(ctx, u.Up); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		index := i
		report, body, err := w.Spend(offer.Key, u.Up, bytes.Repeat([]byte{byte(1 + i)}, 2048), func(body []byte) (uint64, error) {
			n, err := mailbox.EncodedSize(len(body))
			if index == 0 {
				n++
			} // Legacy incorrect physical-byte descriptor.
			return n, err
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.ApplyRemote(u.ID, s.Run(), report); err != nil {
			t.Fatal(err)
		}
		raw, err := mailbox.Encode(body)
		if err != nil {
			t.Fatal(err)
		}
		if err := m.Append(ctx, u.Up, raw); err != nil {
			t.Fatal(err)
		}
	}
	before, err := s.Held(a.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := inventory(ctx, m, u.Up)
	if err != nil {
		t.Fatal(err)
	}
	removed, err := reclaim(ctx, m, s, rows, func(a journal.Attempt) bool { return a.Ordinal > 0 })
	if err != nil {
		t.Fatalf("unprovable old UID blocked all recovery: %v", err)
	}
	if len(removed) != 1 || removed[0].Ordinal != 2 {
		t.Fatalf("removed=%+v, want only second DATA", removed)
	}
	firstAbsent, err := m.Absent(ctx, u.Up, 1, 1)
	if err != nil || firstAbsent {
		t.Fatalf("unproven first UID deleted: %t %v", firstAbsent, err)
	}
	secondAbsent, err := m.Absent(ctx, u.Up, 1, 2)
	if err != nil || !secondAbsent {
		t.Fatalf("healthy UID retained: %t %v", secondAbsent, err)
	}
	after, err := s.Held(a.ID, u.ID)
	if err != nil || after.Bytes == 0 || after.Messages == 0 || after.Bytes >= before.Bytes || after.Messages >= before.Messages {
		t.Fatalf("unproven debt refunded or verified debt not freed: before=%+v after=%+v err=%v", before, after, err)
	}
	if err := s.Audit(); err != nil {
		t.Fatal(err)
	}
	rows, err = inventory(ctx, m, u.Up)
	if err != nil {
		t.Fatal(err)
	}
	again, err := reclaim(ctx, m, s, rows, func(a journal.Attempt) bool { return a.Ordinal > 0 })
	if err != nil || len(again) != 0 {
		t.Fatalf("second scan not idempotent: removed=%+v err=%v", again, err)
	}
	held, err := s.Held(a.ID, u.ID)
	if err != nil || held != after {
		t.Fatalf("second scan changed held quota: %+v vs %+v err=%v", held, after, err)
	}
}
