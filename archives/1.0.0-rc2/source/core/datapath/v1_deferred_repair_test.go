package datapath

import (
	"bytes"
	"testing"
	"time"

	"itom.local/core/flow"
	"itom.local/core/wire"
)

func TestV1EarlyNackSurvivesWriterAndVisibilityGrace(t *testing.T) {
	a, _ := pair(t)
	a.cfg.DeliveryGrace = 500 * time.Millisecond
	item, err := a.Queue(1, 1, []byte("missing-range"))
	if err != nil {
		t.Fatal(err)
	}
	original, err := a.Begin(1, item, "a01", 1000, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Receive(1, control(t, 1, wire.Missing{ID: 1, Ranges: []wire.ByteRange{{Start: 0, End: 13}}})); err != nil {
		t.Fatal(err)
	}
	if a.RepairReady(item) {
		t.Fatal("repair raced original writer")
	}
	if err = a.Complete(1, original.Attempt().ID, flow.Committed); err != nil {
		t.Fatal(err)
	}
	before, _ := a.Snapshot()
	if _, err = a.Poll(1, 499*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if a.RepairReady(item) {
		t.Fatal("repair skipped visibility grace")
	}
	if _, err = a.Poll(1, 500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if !a.RepairReady(item) {
		t.Fatal("authenticated early NACK was discarded; repair needs an unnecessary second probe")
	}
	after, _ := a.Snapshot()
	if before.Logical != after.Logical || before.PhysicalExposure != after.PhysicalExposure {
		t.Fatal("retained NACK changed quota debt")
	}
	repair, err := a.Begin(1, item, "a01", 1000, 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if !repair.Attempt().Repair || !bytes.Equal(repair.frame.Payload, []byte("missing-range")) {
		t.Fatal("repair byte identity changed")
	}
	if err = a.Complete(1, repair.Attempt().ID, flow.Committed); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Receive(1, control(t, 1, wire.Missing{ID: 1, Ranges: []wire.ByteRange{{Start: 0, End: 13}}})); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Poll(1, time.Second); err != nil {
		t.Fatal(err)
	}
	if a.RepairReady(item) {
		t.Fatal("duplicate retained NACK created another repair")
	}
	if err = a.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestV1NackDuringVisibilityGraceNeedsNoSecondProbe(t *testing.T) {
	a, _ := pair(t)
	a.cfg.DeliveryGrace = 500 * time.Millisecond
	item, _ := a.Queue(1, 1, []byte("body"))
	j, err := a.Begin(1, item, "a01", 1000, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Complete(1, j.Attempt().ID, flow.Committed); err != nil {
		t.Fatal(err)
	}
	_, _ = a.Poll(1, 200*time.Millisecond)
	if _, err = a.Receive(1, control(t, 1, wire.Missing{ID: 1, Ranges: []wire.ByteRange{{Start: 1, End: 3}}})); err != nil {
		t.Fatal(err)
	}
	if a.RepairReady(item) {
		t.Fatal("early publication duplicate")
	}
	_, _ = a.Poll(1, 500*time.Millisecond)
	if !a.RepairReady(item) {
		t.Fatal("visibility-grace NACK was discarded")
	}
	if a.pending[item].rangeWanted != (wire.ByteRange{Start: 1, End: 3}) {
		t.Fatal("retained peer intent changed")
	}
	j, err = a.Begin(1, item, "a01", 1000, 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if !j.Attempt().Repair || j.Attempt().Offset != 0 || j.Attempt().PayloadBytes != 4 || !bytes.Equal(j.frame.Payload, []byte("body")) {
		t.Fatal("existing immutable-item repair semantics changed", j.Attempt())
	}
}

func TestV1EarlyNackDoesNotConvertDefinitivelyUnsentOriginal(t *testing.T) {
	a, _ := pair(t)
	item, _ := a.Queue(1, 1, []byte("body"))
	j, err := a.Begin(1, item, "a01", 1000, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Receive(1, control(t, 1, wire.Missing{ID: 1, Ranges: []wire.ByteRange{{Start: 0, End: 4}}}))
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Complete(1, j.Attempt().ID, flow.NotCommitted); err != nil {
		t.Fatal(err)
	}
	_, _ = a.Poll(1, time.Second)
	if a.RepairReady(item) {
		t.Fatal("unsent original converted to repair")
	}
	j, err = a.Begin(1, item, "a01", 1000, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if j.Attempt().Repair {
		t.Fatal("original retry consumed repair permission")
	}
}

func TestV1ReceiptCancelsRetainedNackWithoutRefundingPhysicalDebt(t *testing.T) {
	a, _ := pair(t)
	a.cfg.DeliveryGrace = 500 * time.Millisecond
	item, _ := a.Queue(1, 1, []byte("body"))
	j, err := a.Begin(1, item, "a01", 1000, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Complete(1, j.Attempt().ID, flow.Committed); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Receive(1, control(t, 1, wire.Missing{ID: 1, Ranges: []wire.ByteRange{{Start: 0, End: 4}}})); err != nil {
		t.Fatal(err)
	}
	before, _ := a.Snapshot()
	if _, err = a.Receive(1, control(t, 1, wire.Received{Frontier: 4})); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Poll(1, time.Second); err != nil {
		t.Fatal(err)
	}
	if a.RepairReady(item) {
		t.Fatal("already acknowledged DATA became repairable")
	}
	after, _ := a.Snapshot()
	if before.PhysicalExposure != after.PhysicalExposure {
		t.Fatal("receipt refunded physical debt")
	}
	if err = a.Check(); err != nil {
		t.Fatal(err)
	}
}
