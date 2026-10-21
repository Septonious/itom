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

// A provider header rewrite is recoverable only if the authenticated full DATA
// body, remote grant, exact physical locator and canonical charge all match.
// The physical message must be deleted before the journal releases any debt.
func TestRecoveryRewrittenProviderMIMERequiresPhysicalDeletionProof(t *testing.T) {
	ctx := context.Background()
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
	if err = w.Accept(offer); err != nil {
		t.Fatal(err)
	}
	report, body, err := w.Spend(offer.Key, u.Up, bytes.Repeat([]byte("opaque authenticated DATA"), 250), func(b []byte) (uint64, error) {
		raw, e := mailbox.Encode(b)
		return uint64(len(raw)), e
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyRemote(u.ID, s.Run(), report); err != nil {
		t.Fatal(err)
	}
	raw, err := mailbox.Encode(body)
	if err != nil {
		t.Fatal(err)
	}
	// Preserve the exact decoded secure body and mutate only physical MIME.
	physical := bytes.Replace(raw, []byte("\r\n\r\n"), []byte("\r\nX-Provider-Header: accepted\r\n\r\n"), 1)
	if bytes.Equal(raw, physical) {
		t.Fatal("fixture did not mutate MIME")
	}
	decoded, err := mailbox.Decode(physical)
	if err != nil || !bytes.Equal(decoded, body) {
		t.Fatalf("provider mutation changed plaintext: %v", err)
	}
	m := &memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}
	if err = m.Ensure(ctx, u.Up); err != nil {
		t.Fatal(err)
	}
	if err = m.Append(ctx, u.Up, physical); err != nil {
		t.Fatal(err)
	}
	rows, err := inventory(ctx, m, u.Up)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Held(a.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = reclaim(ctx, m, s, rows, func(a journal.Attempt) bool { return a.Ordinal > 0 })
	if err != nil {
		t.Fatalf("authenticated rewritten MIME must reconcile: %v", err)
	}
	after, err := s.Held(a.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Bytes >= before.Bytes || after.Messages >= before.Messages {
		t.Fatalf("verified physical delete must release proven debt: before=%+v after=%+v", before, after)
	}
	absent, err := m.Absent(ctx, u.Up, 1, 1)
	if err != nil || !absent {
		t.Fatalf("physical message not deleted: absent=%v err=%v", absent, err)
	}
	// A second sweep cannot re-spend, re-observe or double-refund the proof.
	rows, err = inventory(ctx, m, u.Up)
	if err != nil {
		t.Fatal(err)
	}
	_, err = reclaim(ctx, m, s, rows, func(a journal.Attempt) bool { return a.Ordinal > 0 })
	if err != nil {
		t.Fatal(err)
	}
	held, err := s.Held(a.ID, u.ID)
	if err != nil || held != after {
		t.Fatalf("idempotence/refund changed: %+v %v", held, err)
	}
	t.Log("recovered verified rewritten MIME; physical delete precedes durable refund; second sweep idempotent")
}
