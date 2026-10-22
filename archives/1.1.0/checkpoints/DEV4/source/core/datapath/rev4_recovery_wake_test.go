package datapath

import (
	"context"
	"testing"
	"time"

	"itom.local/core/flow"
	"itom.local/core/wire"
)

func TestRev4AuthenticatedNACKRechecksParkedDelivery(t *testing.T) {
	a, _ := pair(t)
	item, err := a.Queue(1, 1, make([]byte, 32<<10))
	if err != nil {
		t.Fatal(err)
	}
	job, err := a.Begin(1, item, "a01", 48<<10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Complete(1, job.Attempt().ID, flow.Committed); err != nil {
		t.Fatal(err)
	}
	p := &Pump{cfg: PumpConfig{Engine: a.cfg, Account: "a01"}, engine: a,
		link: &eventLink{}, ctx: context.Background(), sockets: map[uint64]*socketState{},
		attempted: map[uint64]bool{item: true}, retryWake: map[uint64]<-chan struct{}{item: make(chan struct{})},
		dataBulk: make(chan *Job, 2), dataFast: make(chan *Job, 2), started: time.Now()}
	nack, err := wire.EncodeControl(1, wire.Missing{ID: 1, Ranges: []wire.ByteRange{{Start: 0, End: 32 << 10}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = p.handle(pumpEvent{input: &Input{Generation: 1, Frame: &nack}}); err != nil {
		t.Fatal(err)
	}
	if err = p.progress(); err != nil {
		t.Fatal(err)
	}
	select {
	case repair := <-p.dataBulk:
		if !repair.Attempt().Repair {
			t.Fatal("NACK scheduled an unpermitted original")
		}
		// A repeated NACK cannot unpark work while its writer still owns it.
		blocked := make(chan struct{})
		p.retryWake[item] = blocked
		if err = p.handle(pumpEvent{input: &Input{Generation: 1, Frame: &nack}}); err != nil {
			t.Fatal(err)
		}
		if p.retryWake[item] != blocked {
			t.Fatal("duplicate NACK bypassed live writer ownership")
		}
	default:
		t.Fatal("fresh authenticated repair remained parked behind obsolete admission wake")
	}
}
