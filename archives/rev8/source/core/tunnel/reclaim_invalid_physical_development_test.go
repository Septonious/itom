package tunnel

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"itom.local/core/wallet"
)

// One provider-modified message whose physical proof exceeds the agreed bound
// must not abort recovery of independent, fully authenticated remote DATA.
// The unproven message stays in the mailbox and its durable debt is retained.
func TestReclaimQuarantinesInvalidPhysicalProofWithoutBlockingHealthyData(t *testing.T) {
	ctx := context.Background()
	account := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
	user := servercfg.User{ID: "12345678", Up: "itom-u-up", Down: "itom-u-down"}
	store, err := ledger(t.TempDir(), "data", account, user, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.EnableAfterInventory(account.ID, user.ID, store.Run()); err != nil {
		t.Fatal(err)
	}
	sender, err := wallet.Open(t.TempDir()+"/wallet.db", user.ID, store.StoreID())
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	transport := &memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}
	if err = transport.Ensure(ctx, user.Up); err != nil {
		t.Fatal(err)
	}

	grant, err := store.IssueRemote(journal.Scope{Account: account.ID, User: user.ID, Session: sender.Session()}, dataProfile12)
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
	for i := 0; i < 2; i++ {
		report, body, err := sender.Spend(offer.Key, user.Up, bytes.Repeat([]byte{byte(i + 1)}, 512), func(b []byte) (uint64, error) {
			raw, e := mailbox.Encode(b)
			return uint64(len(raw)), e
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.ApplyRemote(user.ID, store.Run(), report); err != nil {
			t.Fatal(err)
		}
		physical, err := mailbox.Encode(body)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			physical = bytes.Replace(physical, []byte("\r\n\r\n"),
				append([]byte("\r\nX-Provider: "), append(bytes.Repeat([]byte{'X'}, 700), []byte("\r\n\r\n")...)...), 1)
			if n, e := mailbox.CanonicalEncodedBytes(physical); !errors.Is(e, mailbox.ErrLimit) || n != 0 {
				t.Fatalf("invalid physical proof accepted: n=%d err=%v", n, e)
			}
		}
		if err = transport.Append(ctx, user.Up, physical); err != nil {
			t.Fatal(err)
		}
	}
	before, err := store.Held(account.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := inventory(ctx, transport, user.Up)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := reclaim(ctx, transport, store, rows, func(a journal.Attempt) bool { return a.Ordinal > 0 })
	if err != nil {
		t.Fatalf("one invalid proof aborted healthy recovery: %v", err)
	}
	if len(recovered) != 1 {
		t.Fatalf("healthy recovery count=%d want 1", len(recovered))
	}
	after, err := store.Held(account.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Bytes == 0 || after.Messages == 0 || after.Bytes >= before.Bytes || after.Messages >= before.Messages {
		t.Fatalf("unproven debt was refunded or healthy debt not recovered: before=%+v after=%+v", before, after)
	}
	absentBad, err := transport.Absent(ctx, user.Up, 1, 1)
	if err != nil || absentBad {
		t.Fatalf("unproven physical UID was deleted: absent=%v err=%v", absentBad, err)
	}
	absentGood, err := transport.Absent(ctx, user.Up, 1, 2)
	if err != nil || !absentGood {
		t.Fatalf("healthy physical UID was not deleted: absent=%v err=%v", absentGood, err)
	}
	t.Logf("isolated invalid MIME proof: retained debt=%+v, recovered=%d", after, len(recovered))
}
