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

// A bounded GC tick must not repeatedly spend all of its budget on the same
// earlier attempts that have no physical proof, starving new proven DATA.
func TestBoundedReclaimPrioritizesProvenPhysicalRowsWithoutRefundingUnknownDebt(t *testing.T) {
	ctx := context.Background()
	a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
	u := servercfg.User{ID: "12345678", Up: "itom-u-up", Down: "itom-u-down"}
	store, err := ledger(t.TempDir(), "data", a, u, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.EnableAfterInventory(a.ID, u.ID, store.Run()); err != nil {
		t.Fatal(err)
	}
	sender, err := wallet.Open(t.TempDir()+"/wallet.db", u.ID, store.StoreID())
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	grant, err := store.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: sender.Session()}, dataProfile12)
	if err != nil {
		t.Fatal(err)
	}
	offer, err := store.RemoteOffer(grant.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if err = sender.Accept(offer); err != nil {
		t.Fatal(err)
	}
	mail := &memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}
	if err = mail.Ensure(ctx, u.Up); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 34; i++ {
		report, body, e := sender.Spend(offer.Key, u.Up, bytes.Repeat([]byte{byte(i + 1)}, 300), func(b []byte) (uint64, error) { raw, e := mailbox.Encode(b); return uint64(len(raw)), e })
		if e != nil {
			t.Fatal(e)
		}
		if _, e = store.ApplyRemote(u.ID, store.Run(), report); e != nil {
			t.Fatal(e)
		}
		if i == 33 {
			raw, e := mailbox.Encode(body)
			if e != nil {
				t.Fatal(e)
			}
			if e = mail.Append(ctx, u.Up, raw); e != nil {
				t.Fatal(e)
			}
		}
	}
	rows, e := inventory(ctx, mail, u.Up)
	if e != nil {
		t.Fatal(e)
	}
	gone, e := reclaimBounded(ctx, mail, store, rows, func(a journal.Attempt) bool { return a.Ordinal > 0 }, 32)
	if e != nil || len(gone) != 1 || gone[0].Ordinal != 34 {
		t.Fatalf("verified later DATA starved: gone=%v err=%v", gone, e)
	}
	left, e := store.Held(a.ID, u.ID)
	if e != nil || left.Bytes == 0 || left.Messages == 0 {
		t.Fatalf("unproven earlier debts were refunded: %v, %v", left, e)
	}
	if e = store.Audit(); e != nil {
		t.Fatalf("journal audit after bounded cleanup: %v", e)
	}
	t.Logf("bounded cleanup recovered later ordinal=%d while holding older debt=%+v", gone[0].Ordinal, left)
}
