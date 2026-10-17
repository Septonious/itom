package datapath

import (
	"context"
	"itom.local/core/flow"
	"itom.local/core/wire"
	"testing"
	"time"
)

func TestRev3PendingFairInterleavesStreamPrefixes(t *testing.T) {
	a, b := pair(t)
	for _, id := range []uint64{3, 5} {
		if _, err := a.Activate(1, id); err != nil {
			t.Fatal(err)
		}
		w, err := b.Activate(1, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = a.Receive(1, w); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []uint64{1, 3, 5} {
		for n := 0; n < 4; n++ {
			if _, err := a.Queue(1, id, make([]byte, wire.MaxData)); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, after := range []uint64{0, 1, 3, 5} {
		pending := a.PendingFair(after)
		seen := map[uint64]bool{}
		offsets := map[uint64]uint64{}
		for index, item := range pending {
			f, n, err := a.FrameInfo(1, item)
			if err != nil {
				t.Fatal(err)
			}
			if index < 3 && seen[f.Stream] {
				t.Fatalf("one stream monopolizes first writer turn: after=%d stream=%d", after, f.Stream)
			}
			seen[f.Stream] = true
			if f.Offset != offsets[f.Stream] {
				t.Fatal("stream prefix reordered")
			}
			offsets[f.Stream] += n
		}
	}
}

func TestRev3ExhaustedRepairDoesNotStopSiblingScheduling(t *testing.T) {
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
	item, err := a.Queue(1, 1, make([]byte, wire.MaxData))
	if err != nil {
		t.Fatal(err)
	}
	job, err := a.Begin(1, item, "a01", wire.MaxData+100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Complete(1, job.Attempt().ID, flow.CommitUnknown); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 2; n++ {
		a.pending[item].requested = true
		a.pending[item].rangeWanted = wire.ByteRange{Start: 0, End: wire.MaxData}
		job, err = a.Begin(1, item, "a01", wire.MaxData+100, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err = a.Complete(1, job.Attempt().ID, flow.CommitUnknown); err != nil {
			t.Fatal(err)
		}
	}
	a.pending[item].requested = true
	a.pending[item].rangeWanted = wire.ByteRange{Start: 0, End: wire.MaxData}
	if _, err = a.Queue(1, 3, []byte("interactive sibling")); err != nil {
		t.Fatal(err)
	}
	p := &Pump{ctx: context.Background(), engine: a, started: time.Now(), cfg: PumpConfig{Engine: a.cfg, Account: "a01"}, link: &eventLink{}, sockets: map[uint64]*socketState{}, attempted: map[uint64]bool{item: true}, dataBulk: make(chan *Job, 8), dataFast: make(chan *Job, 8)}
	if err = p.progress(); err != nil {
		t.Fatal(err)
	}
	if len(p.dataFast) != 1 {
		t.Fatal("exhausted sibling blocked interactive writer")
	}
	tx, _ := a.Snapshot()
	if tx.Attempts != 4 {
		t.Fatal("unknown debt was lost", tx.Attempts)
	}
}

func TestRev3BulkAttemptReserveAdmitsShortSibling(t *testing.T) {
	cfg := DefaultConfig(1, []string{"a01"})
	cfg.Limits.Attempts = 4
	a, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Stop)
	b, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Stop)
	if _, err = a.Activate(1, 1); err != nil {
		t.Fatal(err)
	}
	initial, err := b.Activate(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Receive(1, initial); err != nil {
		t.Fatal(err)
	}
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
	for i := 0; i < 3; i++ {
		item, err := a.Queue(1, 1, make([]byte, wire.MaxData))
		if err != nil {
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
	if _, err = a.Queue(1, 1, make([]byte, wire.MaxData)); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Queue(1, 3, []byte("probe")); err != nil {
		t.Fatal(err)
	}
	p := &Pump{ctx: context.Background(), engine: a, started: time.Now(), cfg: PumpConfig{Engine: a.cfg, Account: "a01", InteractiveAttemptReserve: 1}, link: &eventLink{}, sockets: map[uint64]*socketState{}, attempted: map[uint64]bool{}, dataBulk: make(chan *Job, 8), dataFast: make(chan *Job, 8)}
	// Existing attempts are not ready and consume the protected physical metadata.
	for id, entry := range a.pending {
		if entry.watch != nil {
			p.attempted[id] = true
		}
	}
	if err = p.progress(); err != nil {
		t.Fatal(err)
	}
	if len(p.dataBulk) != 0 || len(p.dataFast) != 1 {
		t.Fatal("bulk exhausted short attempt reserve")
	}
	tx, _ := a.Snapshot()
	if tx.Attempts != 4 {
		t.Fatal("unknown attempts erased", tx.Attempts)
	}
}
func TestRev3SpeculationBoundsBulkWithoutFragmentingOrBlockingShort(t *testing.T) {
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
	if _, err = a.Queue(1, 1, make([]byte, wire.MaxData)); err != nil {
		t.Fatal(err)
	}
	p := &Pump{engine: a, cfg: PumpConfig{Engine: a.cfg, BulkAdmissionLimit: func() uint64 { return wire.MaxData + 7 }}, sockets: map[uint64]*socketState{}}
	bulk := &socketState{read: &readResult{bulk: true, id: 1, data: make([]byte, wire.MaxData), ack: make(chan struct{})}}
	p.sockets[1] = bulk
	if err = p.acceptRead(1, bulk); err != nil {
		t.Fatal(err)
	}
	if bulk.read == nil || len(bulk.read.data) != wire.MaxData {
		t.Fatal("advisory odd headroom fragmented TCP read")
	}
	short := &socketState{read: &readResult{id: 3, data: []byte("probe"), ack: make(chan struct{})}}
	p.sockets[3] = short
	if err = p.acceptRead(3, short); err != nil {
		t.Fatal(err)
	}
	if short.read != nil {
		t.Fatal("speculation bound blocked short stream")
	}
}
