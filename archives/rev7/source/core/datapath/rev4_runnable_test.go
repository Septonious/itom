package datapath

import (
	"reflect"
	"testing"

	"itom.local/core/flow"
	"itom.local/core/wire"
)

func TestRev4RunnableTurnPreservesDebtAndAuthenticatedRepair(t *testing.T) {
	a, b := pair(t)
	w, err := b.Activate(1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Activate(1, 3); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Receive(1, w); err != nil {
		t.Fatal(err)
	}
	first, err := a.Queue(1, 1, []byte("missing"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.Queue(1, 3, []byte("healthy"))
	if err != nil {
		t.Fatal(err)
	}
	job, err := a.Begin(1, first, "a01", 1024, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := a.ReadyFair(0); !reflect.DeepEqual(got, []uint64{second}) {
		t.Fatal("active writer rescheduled", got)
	}
	if err = a.Complete(1, job.Attempt().ID, flow.CommitUnknown); err != nil {
		t.Fatal(err)
	}
	before, _ := a.Snapshot()
	if got := a.ReadyFair(0); !reflect.DeepEqual(got, []uint64{second}) {
		t.Fatal("unknown completion authorized a blind retry", got)
	}
	if got := a.PendingFair(0); !reflect.DeepEqual(got, []uint64{first, second}) {
		t.Fatal("pending debt disappeared", got)
	}
	nack := control(t, 1, wire.Missing{ID: 1, Ranges: []wire.ByteRange{{Start: 0, End: 7}}})
	if _, err = a.Receive(1, nack); err != nil {
		t.Fatal(err)
	}
	if got := a.ReadyFair(1); !reflect.DeepEqual(got, []uint64{second, first}) {
		t.Fatal("authenticated repair did not regain a fair turn", got)
	}
	after, _ := a.Snapshot()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("scheduler changed charged state", before, after)
	}
	repair, err := a.Begin(1, first, "a01", 1024, 0)
	if err != nil || !repair.Attempt().Repair {
		t.Fatal("repair permission lost", err)
	}
	if got := a.ReadyFair(0); !reflect.DeepEqual(got, []uint64{second}) {
		t.Fatal("active repair duplicated", got)
	}
}
