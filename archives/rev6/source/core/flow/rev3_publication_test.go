package flow

import "testing"

func TestRev3UnpublishedHandoffCannotAdvertiseMissingBytes(t *testing.T) {
	tx := transmitter(t)
	_, a := begin(t, tx)
	if n, err := tx.AttemptedEnd(a.Stream); err != nil || n != 0 {
		t.Fatalf("queued worker advertises unpublished bytes: %d %v", n, err)
	}
	if err := tx.Complete(a.ID, NotCommitted); err != nil {
		t.Fatal(err)
	}
	if n, err := tx.AttemptedEnd(a.Stream); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}

func TestRev3UnknownPublicationStillAdvertisesAndRetainsDebt(t *testing.T) {
	tx := transmitter(t)
	_, a := begin(t, tx)
	if err := tx.Complete(a.ID, CommitUnknown); err != nil {
		t.Fatal(err)
	}
	if n, err := tx.AttemptedEnd(a.Stream); err != nil || n != a.Offset+a.PayloadBytes {
		t.Fatal(n, err)
	}
	if tx.Snapshot().PhysicalExposure != a.EncodedBytes {
		t.Fatal("unknown debt was refunded")
	}
}
