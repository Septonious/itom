package wallet

import (
	"errors"
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"

	"itom.local/core/wire"
)

func TestRev4ReorderedOfferDoesNotRenewRetiredAuthority(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wallet.db")
	w, err := Open(path, "12345678", wire.ID{1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { w.Close() }()
	offer := func(id uint64) wire.StorageGrant {
		return wire.StorageGrant{Key: wire.QuotaKey{Store: wire.ID{1}, Epoch: 1, Grant: id,
			Session: w.Session(), User: "12345678", Account: "a01"}, Limit: wire.Quota{Bytes: 10000, Messages: 10}}
	}
	high, late := offer(18), offer(14)
	if err = w.Accept(high); err != nil {
		t.Fatal(err)
	}
	if accepted, err := w.AcceptCurrentOffer(late); err != nil || !accepted {
		t.Fatal("unseen authentic offer was rejected solely because a newer UID became visible first", accepted, err)
	}
	usage, _, err := w.Spend(late.Key, "up", []byte("encrypted"), func([]byte) (uint64, error) { return 1000, nil })
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Accept(late); err != nil {
		t.Fatal(err)
	}
	entries, err := w.Entries()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Offer.Key == late.Key && entry.Used != usage.Total {
			t.Fatal("duplicate offer renewed lifetime usage")
		}
	}
	if _, err = w.Fence(late.Key); err != nil {
		t.Fatal(err)
	}
	status := wire.CleanupStatus{Key: late.Key, Used: usage.Total, Registered: usage.Total,
		Released: usage.Total, Closed: true}
	if err = w.Acknowledge(status); err != nil {
		t.Fatal(err)
	}
	if err = w.CompleteRetirement(wire.Retirement{Key: late.Key, Final: usage.Total, Ack: true}); err != nil {
		t.Fatal(err)
	}
	if accepted, err := w.AcceptCurrentOffer(late); err != nil || accepted {
		t.Fatal("retired replay became spendable", accepted, err)
	}
	if _, _, err = w.Spend(late.Key, "up", []byte("another APPEND"), func([]byte) (uint64, error) { return 1000, nil }); !errors.Is(err, ErrState) {
		t.Fatal("retired authority was recreated", err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	// Simulate a legacy wallet that predates the retirement metadata.
	legacy, err := bolt.Open(path, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = legacy.Update(func(tx *bolt.Tx) error {
		if err := tx.DeleteBucket(retiredOffers); err != nil {
			return err
		}
		return tx.Bucket(meta).Delete([]byte("offer_floor"))
	}); err != nil {
		t.Fatal(err)
	}
	if err = legacy.Close(); err != nil {
		t.Fatal(err)
	}
	w, err = Open(path, "12345678", wire.ID{1})
	if err != nil {
		t.Fatal(err)
	}
	// The new session cannot reopen unknown historical IDs after tombstone reset.
	if accepted, err := w.AcceptCurrentOffer(offer(15)); err != nil || accepted {
		t.Fatal("restart manufactured authority below the durable historical floor", accepted, err)
	}
}

func TestRev4RetiredOfferBoundKeepsReplayFencedAfterPruning(t *testing.T) {
	w, err := Open(filepath.Join(t.TempDir(), "wallet.db"), "12345678", wire.ID{1})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	// A checkpoint of completed retirements avoids thousands of unrelated fsyncs.
	if err = w.write(func(tx *bolt.Tx) error {
		for id := uint64(1); id <= retiredOfferLimit; id++ {
			if err := tx.Bucket(retiredOffers).Put(key(id), []byte{1}); err != nil {
				return err
			}
		}
		return tx.Bucket(meta).Put([]byte("high"), key(retiredOfferLimit))
	}); err != nil {
		t.Fatal(err)
	}
	o := wire.StorageGrant{Key: wire.QuotaKey{Store: wire.ID{1}, Epoch: 1, Grant: retiredOfferLimit + 1,
		Session: w.Session(), User: "12345678", Account: "a01"}, Limit: wire.Quota{Bytes: 1000, Messages: 1}}
	if err = w.Accept(o); err != nil {
		t.Fatal(err)
	}
	if _, err = w.Fence(o.Key); err != nil {
		t.Fatal(err)
	}
	if err = w.Acknowledge(wire.CleanupStatus{Key: o.Key, Closed: true}); err != nil {
		t.Fatal(err)
	}
	if err = w.CompleteRetirement(wire.Retirement{Key: o.Key, Ack: true}); err != nil {
		t.Fatal(err)
	}
	if err = w.db.View(func(tx *bolt.Tx) error {
		if tx.Bucket(retiredOffers).Stats().KeyN > retiredOfferLimit {
			t.Fatal("retired-offer memory/disk bound exceeded")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	o.Key.Grant = 1
	if accepted, err := w.AcceptCurrentOffer(o); err != nil || accepted {
		t.Fatal("pruned retirement became reusable authority", accepted, err)
	}
}
