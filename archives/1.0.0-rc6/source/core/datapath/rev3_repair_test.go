package datapath

import (
	"itom.local/core/flow"
	"itom.local/core/wire"
	"testing"
)

func TestRev3UnpublishedRepairDoesNotConsumePhysicalRetryBudget(t *testing.T) {
	a, _ := pair(t)
	item, err := a.Queue(1, 1, make([]byte, wire.MaxData))
	if err != nil {
		t.Fatal(err)
	}
	original, err := a.Begin(1, item, "a01", wire.MaxData+100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Complete(1, original.Attempt().ID, flow.Committed); err != nil {
		t.Fatal(err)
	}
	for id := uint64(1); id <= 6; id++ {
		f := control(t, 1, wire.Missing{ID: id, Ranges: []wire.ByteRange{{Start: 0, End: wire.MaxData}}})
		if _, err = a.Receive(1, f); err != nil {
			t.Fatal(err)
		}
		if !a.RepairReady(item) {
			t.Fatalf("pre-APPEND deferrals exhausted repair at NACK %d", id)
		}
		j, err := a.Begin(1, item, "a01", wire.MaxData+100, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err = a.Complete(1, j.Attempt().ID, flow.NotCommitted); err != nil {
			t.Fatal(err)
		}
		if !a.RepairReady(item) {
			t.Fatal("unpublished authorized repair waits for another two-second NACK")
		}

	}
	for id := uint64(7); id <= 8; id++ {
		if _, err = a.Receive(1, control(t, 1, wire.Missing{ID: id, Ranges: []wire.ByteRange{{Start: 0, End: wire.MaxData}}})); err != nil {
			t.Fatal(err)
		}
		j, err := a.Begin(1, item, "a01", wire.MaxData+100, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err = a.Complete(1, j.Attempt().ID, flow.CommitUnknown); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = a.Receive(1, control(t, 1, wire.Missing{ID: 9, Ranges: []wire.ByteRange{{Start: 0, End: wire.MaxData}}})); err != nil {
		t.Fatal(err)
	}
	if a.RepairReady(item) {
		t.Fatal("unknown publications bypassed physical retry bound")
	}
	tx, _ := a.Snapshot()
	if tx.Attempts != 3 || tx.Logical != wire.MaxData {
		t.Fatal("pre-APPEND yield changed unknown physical or logical debt", tx)
	}
}
