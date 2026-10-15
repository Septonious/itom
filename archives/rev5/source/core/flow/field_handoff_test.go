package flow

import "testing"

func TestFieldUnsentGrantHandoffsDoNotConsumePhysicalAttempts(t *testing.T) {
	tx := transmitter(t)
	id, a := begin(t, tx)
	for i := 0; i < 5; i++ {
		if err := tx.Complete(a.ID, NotCommitted); err != nil {
			t.Fatal(err)
		}
		if err := tx.Complete(a.ID, NotCommitted); err != nil {
			t.Fatal("duplicate completion", err)
		}
		if err := tx.Reserve(id, "b"); err != nil {
			t.Fatal(err)
		}
		var err error
		a, err = tx.Begin(id, 256)
		if err != nil {
			t.Fatal("unsent handoff consumed retry budget", i, err)
		}
	}
	if err := tx.Complete(a.ID, CommitUnknown); err != nil {
		t.Fatal(err)
	}
	if tx.Snapshot().PhysicalExposure != 256 {
		t.Fatal("unknown physical debt was refunded")
	}
	if err := tx.Check(); err != nil {
		t.Fatal(err)
	}
}
