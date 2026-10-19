package tunnel

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"itom.local/core/datapath"
	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
	"itom.local/core/wallet"
	"itom.local/core/wire"
)

// Retained debt plus live bulk reservations must leave room to replace a spent
// fast grant before its physical messages are deleted. A fence returns UNUSED
// permission only; all spent bytes/messages remain charged throughout this test.
func TestRC12RetainedFastRefillDoesNotWaitForPhysicalDeletion(t *testing.T) {
	a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
	u := servercfg.User{ID: "12345678", Up: "up", Down: "down"}
	s, err := journal.Open(filepath.Join(t.TempDir(), "data.db"), ledgerConfig(a, u, dataProfile12), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
		t.Fatal(err)
	}
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
	var fast wire.StorageGrant
	bulk := map[wire.QuotaKey]journal.Grant{}
	for len(l.urgentQueue) > 0 {
		c, err := wire.DecodeControl((<-l.urgentQueue).Frame)
		if err != nil {
			t.Fatal(err)
		}
		var g wire.StorageGrant
		switch x := c.(type) {
		case wire.StorageGrant:
			g = x
		case wire.RecoveryFastGrant:
			g = x.Offer
		default:
			continue
		}
		if l.issuedFast[g.Key] {
			fast = g
		} else {
			grant, err := s.GetGrant(journal.GrantRef{Store: g.Key.Store, ID: g.Key.Grant, Scope: journal.Scope{Account: a.ID, User: u.ID, Session: g.Key.Session}})
			if err != nil {
				t.Fatal(err)
			}
			bulk[g.Key] = grant
		}
	}
	if fast.Key.Grant == 0 || len(bulk) != 2 {
		t.Fatal("missing initial classes", fast, len(bulk))
	}
	if err = w.Accept(fast); err != nil {
		t.Fatal(err)
	}
	for i := uint64(0); i < fast.Limit.Messages; i++ {
		report, _, err := w.Spend(fast.Key, u.Up, bytes.Repeat([]byte{byte(i)}, 256), func(b []byte) (uint64, error) { return mailbox.EncodedSize(len(b)) })
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.ApplyRemote(u.ID, s.Run(), report); err != nil {
			t.Fatal(err)
		}
	}
	release, err := w.Fence(fast.Key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyRemote(u.ID, s.Run(), release); err != nil {
		t.Fatal(err)
	}
	l.issued[fast.Key] = false
	if err = l.offer(); err != nil {
		t.Fatal(err)
	}
	var replacement wire.StorageGrant
	for len(l.urgentQueue) > 0 {
		c, err := wire.DecodeControl((<-l.urgentQueue).Frame)
		if err != nil {
			t.Fatal(err)
		}
		switch x := c.(type) {
		case wire.StorageGrant:
			if l.issuedFast[x.Key] {
				replacement = x
			}
		case wire.RecoveryFastGrant:
			replacement = x.Offer
		}
	}
	if replacement.Key.Grant == 0 || replacement.Limit.Messages < 4 {
		t.Fatalf("short traffic stalls or falls into one-message fencing before any deletion: initial=%+v replacement=%+v", fast.Limit, replacement.Limit)
	}
	// Original unknown debt and every old spent physical message are unchanged.
	got, err := s.GetGrant(old.Ref)
	if err != nil || got != old {
		t.Fatal("historical debt changed", err)
	}
	previous, err := s.GetGrant(journal.GrantRef{Store: fast.Key.Store, ID: fast.Key.Grant, Scope: journal.Scope{Account: a.ID, User: u.ID, Session: fast.Key.Session}})
	if err != nil || previous.Released != (pool.Amount{}) || previous.Used.Messages != fast.Limit.Messages {
		t.Fatal("unproved physical debt refunded", previous, err)
	}
	for _, b := range bulk {
		got, err := s.GetGrant(b.Ref)
		if err != nil || got != b || b.Limit.Bytes < 2*dataBatchMIMEHardCap {
			t.Fatal("bulk grant reduced or revoked", got, err)
		}
	}
	if err = s.Audit(); err != nil {
		t.Fatal(err)
	}
}

// Exercise actual sealed records and the authenticated envelope, rather than
// assuming that a raw payload size is the physical storage size.
func TestRC12ReducedFastGrantFitsMaximumFastBatch(t *testing.T) {
	jobs := deliveryJobs(t, 1, 4, datapath.InteractiveDataThreshold)
	cipher := cleanupFixtureEpoch(t)
	var packets [][]byte
	for _, job := range jobs {
		if !job.Interactive() {
			t.Fatal("fixture is not a fast batch")
		}
		result, packet, _ := job.SealForBatch(cipher)
		if result.Err != nil {
			t.Fatal(result.Err)
		}
		packets = append(packets, packet)
	}
	body, err := wire.PackDataBatch(wire.ID{1}, packets)
	if err != nil {
		t.Fatal(err)
	}
	key := wire.QuotaKey{Store: wire.ID{2}, Epoch: 1, Grant: 1, Session: cipher.Context().Session, User: "12345678", Account: "a01"}
	_, physical, err := wire.BindDataEnvelope(key, 1, wire.ID{3}, 0, "up", body, bytes.Repeat([]byte{7}, 32), canonicalDataSize)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := mailbox.Encode(physical)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > recoveryFastLeaseBytes/2 {
		t.Fatal("two maximum fast batches no longer fit recovery grant", len(raw))
	}
	offer := wire.StorageGrant{Key: key, Limit: wire.Quota{Bytes: recoveryFastLeaseBytes, Messages: 32}}
	frame, err := wire.EncodeControl(0, wire.RecoveryFastGrant{Offer: offer})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := wire.DecodeControl(frame)
	if err != nil || decoded.(wire.RecoveryFastGrant).Offer != offer {
		t.Fatal("explicit class or quota changed", err)
	}
}
