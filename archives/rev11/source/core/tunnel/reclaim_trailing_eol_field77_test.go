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

// Regression for provider-added blank lines that preserve the encrypted DATA
// body but previously changed the byte charge, blocking delivery and durable
// GC. A valid physical deletion is still required before releasing any quota.
func TestField77ProviderTrailingCRLFReclaimRequiresSameUIDDeletion(t *testing.T) {
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
	report, body, err := w.Spend(offer.Key, u.Up, bytes.Repeat([]byte("itd3-physical-body"), 50), func(b []byte) (uint64, error) { return mailbox.EncodedSize(len(b)) }) // test uses physical ciphertext as opaque data
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
	physical := append(bytes.Clone(raw), []byte("\r\n\r\n")...)
	saved := bytes.Clone(physical)
	mail := &memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}
	if err = mail.Ensure(ctx, u.Up); err != nil {
		t.Fatal(err)
	}
	if err = mail.Append(ctx, u.Up, physical); err != nil {
		t.Fatal(err)
	}
	before, err := s.Held(a.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.Bytes == 0 {
		t.Fatal("no durable charge")
	}
	rows, err := inventory(ctx, mail, u.Up)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reclaim(ctx, mail, s, rows, func(a journal.Attempt) bool { return a.Ordinal > 0 }); err != nil {
		t.Fatalf("valid normalized physical evidence refused: %v", err)
	}
	after, err := s.Held(a.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Bytes >= before.Bytes || after.Messages >= before.Messages {
		t.Fatalf("deletion proof failed to refund: before=%+v after=%+v", before, after)
	}
	absent, err := mail.Absent(ctx, u.Up, 1, 1)
	if err != nil || !absent {
		t.Fatalf("physical UID not deleted: absent=%v err=%v", absent, err)
	}
	if !bytes.Equal(physical, saved) {
		t.Fatal("physical evidence rewritten")
	}
	if err = s.Audit(); err != nil {
		t.Fatal(err)
	}
}
