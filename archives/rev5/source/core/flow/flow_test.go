package flow

import (
	"bytes"
	"errors"
	"testing"

	"itom.local/core/wire"
)

func transmitter(t *testing.T) *Tx {
	t.Helper()
	tx, err := NewTx(DefaultLimits(), map[string]uint64{"a": 1 << 20, "b": 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.OpenStream(1); err != nil {
		t.Fatal(err)
	}
	if err = tx.ConnectionLimit(12 << 20); err != nil {
		t.Fatal(err)
	}
	if err = tx.StreamLimit(1, 1<<20); err != nil {
		t.Fatal(err)
	}
	return tx
}
func begin(t *testing.T, tx *Tx) (uint64, Attempt) {
	t.Helper()
	id, err := tx.Admit(1, []byte("abcd"))
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Reserve(id, "a"); err != nil {
		t.Fatal(err)
	}
	a, err := tx.Begin(id, 256)
	if err != nil {
		t.Fatal(err)
	}
	return id, a
}
func checkReceiptBeforeAppendCompletion(t *testing.T) {
	tx := transmitter(t)
	_, a := begin(t, tx)
	n, err := tx.Receipt(1, 4)
	if err != nil || n != 4 {
		t.Fatal(n, err)
	}
	if err = tx.Complete(a.ID, CommitUnknown); err != nil {
		t.Fatal(err)
	}
	s := tx.Snapshot()
	if s.Logical != 0 || s.PhysicalExposure != 256 {
		t.Fatal(s)
	}
	if _, err = tx.Receipt(1, 4); err != nil {
		t.Fatal(err)
	}
	if err = tx.Cleanup(a.ID); err != nil {
		t.Fatal(err)
	}
	if tx.Snapshot().Attempts != 0 {
		t.Fatal("leaked attempt")
	}
	if err = tx.Check(); err != nil {
		t.Fatal(err)
	}
}
func checkFailedAppendCanReturnToQueue(t *testing.T) {
	tx := transmitter(t)
	id, a := begin(t, tx)
	if err := tx.Complete(a.ID, NotCommitted); err != nil {
		t.Fatal(err)
	}
	s := tx.Snapshot()
	if s.Stages[Queued] != 4 || s.AccountDebt["a"] != 0 || s.Attempts != 0 {
		t.Fatal(s)
	}
	if err := tx.Reserve(id, "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Begin(id, 256); err != nil {
		t.Fatal(err)
	}
}
func checkUnknownAppendRetainsDebt(t *testing.T) {
	tx := transmitter(t)
	_, a := begin(t, tx)
	if err := tx.Complete(a.ID, CommitUnknown); err != nil {
		t.Fatal(err)
	}
	s := tx.Snapshot()
	if s.Stages[VisibleWait] != 4 || s.AccountDebt["a"] != 4 || s.PhysicalExposure != 256 {
		t.Fatal(s)
	}
	if err := tx.Complete(a.ID, NotCommitted); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}
func checkCleanupEvidenceSurvivesFailedRepair(t *testing.T) {
	tx := transmitter(t)
	id, a := begin(t, tx)
	if err := tx.Complete(a.ID, Committed); err != nil {
		t.Fatal(err)
	}
	if err := tx.Cleanup(a.ID); err != nil {
		t.Fatal(err)
	}
	b, err := tx.BeginRepair(id, "b", 256, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Complete(b.ID, NotCommitted); err != nil {
		t.Fatal(err)
	}
	if tx.Snapshot().Stages[VisibleWait] != 4 {
		t.Fatal("forgot known commit")
	}
	if _, err = tx.Receipt(1, 4); err != nil {
		t.Fatal(err)
	}
	if err = tx.Check(); err != nil {
		t.Fatal(err)
	}
}
func checkResetDoesNotForgivePhysicalExposure(t *testing.T) {
	tx := transmitter(t)
	_, a := begin(t, tx)
	if err := tx.ForgetStream(1); err != nil {
		t.Fatal(err)
	}
	if err := tx.Complete(a.ID, Committed); err != nil {
		t.Fatal(err)
	}
	s := tx.Snapshot()
	if s.Logical != 0 || s.PhysicalExposure != 256 {
		t.Fatal(s)
	}
}
func checkReceiptCannotCrossUnstartedDataOrPartialRecord(t *testing.T) {
	tx := transmitter(t)
	begin(t, tx)
	if _, err := tx.Admit(1, []byte("efgh")); err != nil {
		t.Fatal(err)
	}
	for _, n := range []uint64{2, 8, 9} {
		if _, err := tx.Receipt(1, n); !errors.Is(err, ErrReceipt) {
			t.Fatalf("%d: %v", n, err)
		}
	}
	if tx.Snapshot().Logical != 8 {
		t.Fatal("invalid receipt mutated ledger")
	}
}
func checkRepairLimitDoesNotDiscardData(t *testing.T) {
	tx := transmitter(t)
	id, a := begin(t, tx)
	if err := tx.Complete(a.ID, CommitUnknown); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		x, err := tx.BeginRepair(id, "b", 256, 0, 4)
		if err != nil {
			t.Fatal(err)
		}
		if err = tx.Complete(x.ID, CommitUnknown); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.BeginRepair(id, "b", 256, 0, 4); !errors.Is(err, ErrCredit) {
		t.Fatal(err)
	}
	if tx.Snapshot().Logical != 4 {
		t.Fatal("premature discard after third attempt")
	}
}
func checkSenderWaitsForBothAbsoluteWindows(t *testing.T) {
	tx, err := NewTx(DefaultLimits(), map[string]uint64{"a": 1024})
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.OpenStream(1); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Admit(1, []byte{1}); !errors.Is(err, ErrCredit) {
		t.Fatal(err)
	}
	if err = tx.ConnectionLimit(2); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Admit(1, []byte{1}); !errors.Is(err, ErrCredit) {
		t.Fatal(err)
	}
	if err = tx.StreamLimit(1, 2); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Admit(1, []byte{1, 2}); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Admit(1, []byte{3}); !errors.Is(err, ErrCredit) {
		t.Fatal(err)
	}
	if err = tx.ConnectionLimit(1); !errors.Is(err, ErrCredit) {
		t.Fatal(err)
	}
}
func receiver(t *testing.T, cap uint64) *Rx {
	t.Helper()
	r, err := NewRx(cap, 128, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.OpenStream(1, 1024); err != nil {
		t.Fatal(err)
	}
	return r
}
func data(offset uint64, text string) wire.Frame {
	return wire.Frame{Type: wire.Data, Stream: 1, Logical: offset + 1, Offset: offset, Payload: []byte(text)}
}
func checkOutOfOrderReceiptAndPartialWriteDuplicate(t *testing.T) {
	r := receiver(t, 16)
	if frontier, _, err := r.Receive(data(4, "efgh")); err != nil || frontier != 0 {
		t.Fatal(frontier, err)
	}
	if frontier, _, err := r.Receive(data(0, "abcd")); err != nil || frontier != 8 {
		t.Fatal(frontier, err)
	}
	if err := r.Delivered(1, 2); err != nil {
		t.Fatal(err)
	}
	if _, duplicate, err := r.Receive(data(0, "abcd")); err != nil || !duplicate {
		t.Fatal(duplicate, err)
	}
	if _, _, err := r.Receive(data(0, "abXd")); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	got, err := r.Peek(1, 4)
	if err != nil || !bytes.Equal(got, []byte("cd")) {
		t.Fatal(string(got), err)
	}
	if err = r.Check(); err != nil {
		t.Fatal(err)
	}
}
func checkHolesConsumeConnectionCredit(t *testing.T) {
	r := receiver(t, 8)
	if _, _, err := r.Receive(data(7, "h")); err != nil {
		t.Fatal(err)
	}
	if err := r.OpenStream(3, 1024); err != nil {
		t.Fatal(err)
	}
	f := data(0, "z")
	f.Stream = 3
	if _, _, err := r.Receive(f); !errors.Is(err, ErrCredit) {
		t.Fatal(err)
	}
	if _, _, err := r.Receive(data(0, "abcdefg")); err != nil {
		t.Fatal(err)
	}
	if err := r.Delivered(1, 7); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Receive(f); err != nil {
		t.Fatal(err)
	}
	if err := r.Check(); err != nil {
		t.Fatal(err)
	}
}
func checkFinWaitsForDeliveryAndResetReplenishesOnce(t *testing.T) {
	r := receiver(t, 8)
	if _, _, err := r.Receive(data(0, "abcd")); err != nil {
		t.Fatal(err)
	}
	if err := r.Fin(1, 4); err != nil {
		t.Fatal(err)
	}
	if r.HalfClosed(1) {
		t.Fatal("half closed before delivery")
	}
	if err := r.Reset(1, 5); !errors.Is(err, ErrConflict) {
		t.Fatal("changed known final offset", err)
	}
	if err := r.Delivered(1, 4); err != nil {
		t.Fatal(err)
	}
	if !r.HalfClosed(1) {
		t.Fatal("missing half close")
	}
	if err := r.Reset(1, 4); err != nil {
		t.Fatal(err)
	}
	s := r.Snapshot()
	if err := r.Reset(1, 4); !errors.Is(err, ErrUnknown) {
		t.Fatal(err)
	}
	if s != r.Snapshot() {
		t.Fatal("double credit")
	}
	if err := r.Check(); err != nil {
		t.Fatal(err)
	}
}

// Resource-independent checks form one scenario; lifecycle and concurrency
// checks in this file remain independently scheduled.
func TestScenarioFlow(t *testing.T) {
	t.Log("check=checkReceiptBeforeAppendCompletion")
	checkReceiptBeforeAppendCompletion(t)
	t.Log("check=checkFailedAppendCanReturnToQueue")
	checkFailedAppendCanReturnToQueue(t)
	t.Log("check=checkUnknownAppendRetainsDebt")
	checkUnknownAppendRetainsDebt(t)
	t.Log("check=checkCleanupEvidenceSurvivesFailedRepair")
	checkCleanupEvidenceSurvivesFailedRepair(t)
	t.Log("check=checkResetDoesNotForgivePhysicalExposure")
	checkResetDoesNotForgivePhysicalExposure(t)
	t.Log("check=checkReceiptCannotCrossUnstartedDataOrPartialRecord")
	checkReceiptCannotCrossUnstartedDataOrPartialRecord(t)
	t.Log("check=checkRepairLimitDoesNotDiscardData")
	checkRepairLimitDoesNotDiscardData(t)
	t.Log("check=checkSenderWaitsForBothAbsoluteWindows")
	checkSenderWaitsForBothAbsoluteWindows(t)
	t.Log("check=checkOutOfOrderReceiptAndPartialWriteDuplicate")
	checkOutOfOrderReceiptAndPartialWriteDuplicate(t)
	t.Log("check=checkHolesConsumeConnectionCredit")
	checkHolesConsumeConnectionCredit(t)
	t.Log("check=checkFinWaitsForDeliveryAndResetReplenishesOnce")
	checkFinWaitsForDeliveryAndResetReplenishesOnce(t)
}
