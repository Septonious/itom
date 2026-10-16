package datapath

import (
	"itom.local/core/flow"
	"itom.local/core/wire"
	"testing"
)

func TestResetPreservesPhysicalDebtAndOtherStream(t *testing.T) {
	a, b := pair(t)
	wa, e := a.Activate(1, 3)
	if e != nil {
		t.Fatal(e)
	}
	wb, e := b.Activate(1, 3)
	if e != nil {
		t.Fatal(e)
	}
	a.Receive(1, wb)
	b.Receive(1, wa)
	item, e := a.Queue(1, 1, []byte("pending"))
	if e != nil {
		t.Fatal(e)
	}
	job, e := a.Begin(1, item, "a01", 512, 0)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.Complete(1, job.Attempt().ID, flow.Committed); e != nil {
		t.Fatal(e)
	}
	reset, e := a.Abort(1, 1, wire.ReasonClosed)
	if e != nil {
		t.Fatal(e)
	}
	replies, e := b.Receive(1, reset)
	if e != nil || len(replies) != 1 {
		t.Fatal(e)
	}
	if _, e = a.Receive(1, replies[0]); e != nil {
		t.Fatal(e)
	}
	before, _ := a.Snapshot()
	if before.Attempts == 0 {
		t.Fatal("RESET erased physical exposure")
	}
	if _, e = a.Queue(1, 3, []byte("unaffected")); e != nil {
		t.Fatal("other stream lost", e)
	}
	if e = a.Cleaned(1, job.Attempt().ID); e != nil {
		t.Fatal(e)
	}
	if _, e = b.Receive(1, reset); e != nil {
		t.Fatal("duplicate RESET", e)
	}
	if e = a.Check(); e != nil {
		t.Fatal(e)
	}
	if e = b.Check(); e != nil {
		t.Fatal(e)
	}
}
