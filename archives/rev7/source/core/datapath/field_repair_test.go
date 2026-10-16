package datapath

import (
	"bytes"
	"testing"
	"time"

	"itom.local/core/flow"
	"itom.local/core/wire"
)

// STATUS can retain attempted offsets after a definitive pre-APPEND yield.
// Such an item owns an ORIGINAL queued send, not a physically published repair.
// A late NACK must not call BeginRepair on Queued and terminate the owner.
func TestNackAfterDefinitiveYieldKeepsQueuedOriginalSend(t *testing.T) {
	a, _ := pair(t)
	payload := []byte("original queued after admission yield")
	id, err := a.Queue(1, 1, payload)
	if err != nil {
		t.Fatal(err)
	}
	job, err := a.Begin(1, id, "a01", 1000, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Complete(1, job.attempt.ID, flow.NotCommitted); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Poll(1, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	nack := control(t, 1, wire.Missing{ID: 1, Ranges: []wire.ByteRange{{Start: 0, End: uint64(len(payload))}}})
	if _, err = a.Receive(1, nack); err != nil {
		t.Fatal(err)
	}
	if a.RepairReady(id) {
		t.Fatal("unsent queued original converted to repair")
	}
	job, err = a.Begin(1, id, "a01", 1000, 2*time.Second)
	if err != nil {
		t.Fatal("late NACK killed original retry", err)
	}
	if job.Attempt().Repair || job.frame.Offset != 0 || !bytes.Equal(job.frame.Payload, payload) {
		t.Fatal("original byte identity changed")
	}
	if err = a.Complete(1, job.attempt.ID, flow.Committed); err != nil {
		t.Fatal(err)
	}
	if err = a.Check(); err != nil {
		t.Fatal(err)
	}
}

// In the field-shaped IMAP run, STATUS overtakes DATA waiting for physical
// grants/APPEND; a valid peer NACK must not create a second physical attempt
// while the original is still in flight. Successful APPEND also needs a short
// BODY visibility grace. Neither delay is evidence of loss or quota cleanup.
func TestFieldStatusOvertakingDataDoesNotAmplifyPendingAppend(t *testing.T) {
	a, _ := pair(t)
	a.cfg.DeliveryGrace = 500 * time.Millisecond
	id, err := a.Queue(1, 1, []byte("pending-body"))
	if err != nil {
		t.Fatal(err)
	}
	job, err := a.Begin(1, id, "a01", 1000, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Poll(1, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	nack := control(t, 1, wire.Missing{ID: 1, Ranges: []wire.ByteRange{{Start: 0, End: 12}}})
	before, _ := a.Snapshot()
	if _, err = a.Receive(1, nack); err != nil {
		t.Fatal(err)
	}
	if a.RepairReady(id) {
		t.Fatal("STATUS/NACK amplified an original APPEND still in flight")
	}
	after, _ := a.Snapshot()
	if before.PhysicalExposure != after.PhysicalExposure || before.Logical != after.Logical {
		t.Fatal("ignored early NACK changed debt")
	}
	if err = a.Complete(1, job.attempt.ID, flow.Committed); err != nil {
		t.Fatal(err)
	}
	_, _ = a.Poll(1, 2499*time.Millisecond)
	if _, err = a.Receive(1, control(t, 1, wire.Missing{ID: 2, Ranges: []wire.ByteRange{{Start: 0, End: 12}}})); err != nil {
		t.Fatal(err)
	}
	if a.RepairReady(id) {
		t.Fatal("accepted BODY visibility grace was skipped")
	}
	_, _ = a.Poll(1, 2500*time.Millisecond)
	if _, err = a.Receive(1, control(t, 1, wire.Missing{ID: 3, Ranges: []wire.ByteRange{{Start: 0, End: 12}}})); err != nil {
		t.Fatal(err)
	}
	if !a.RepairReady(id) {
		t.Fatal("deferred loss could not recover after bounded grace")
	}
	job, err = a.Begin(1, id, "a01", 1000, 2500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Complete(1, job.attempt.ID, flow.CommitUnknown); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Receive(1, control(t, 1, wire.Missing{ID: 4, Ranges: []wire.ByteRange{{Start: 0, End: 12}}})); err != nil {
		t.Fatal(err)
	}
	if !a.RepairReady(id) {
		t.Fatal("unknown APPEND was delayed as if successfully published")
	}
	if err = a.Check(); err != nil {
		t.Fatal(err)
	}
}
