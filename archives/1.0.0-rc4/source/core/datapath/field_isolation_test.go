package datapath

import (
	"context"
	"itom.local/core/flow"
	"itom.local/core/wire"
	"testing"
	"time"
)

func TestFieldExpiredStreamKeepsSiblingAndPhysicalDebt(t *testing.T) {
	a, b := pair(t)
	a.cfg.IsolateStreamTimeouts = true
	a.cfg.Deadline = time.Second
	wa, err := a.Activate(1, 3)
	if err != nil {
		t.Fatal(err)
	}
	wb, err := b.Activate(1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Receive(1, wb); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Receive(1, wa); err != nil {
		t.Fatal(err)
	}
	item, err := a.Queue(1, 1, []byte("undelivered"))
	if err != nil {
		t.Fatal(err)
	}
	job, err := a.Begin(1, item, "a01", 512, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Complete(1, job.Attempt().ID, flow.CommitUnknown); err != nil {
		t.Fatal(err)
	}
	before, _ := a.Snapshot()
	frames, err := a.Poll(1, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var reset wire.Frame
	for _, f := range frames {
		if f.Type == wire.Reset && f.Stream == 1 {
			reset = f
		}
	}
	if reset.Type != wire.Reset || !a.Aborted(1) {
		t.Fatal("expired stream was not reset")
	}
	after, _ := a.Snapshot()
	if after.PhysicalExposure != before.PhysicalExposure || after.Attempts != before.Attempts {
		t.Fatal("timeout refunded unproven physical debt")
	}
	// Lose RESET/ACK for another full deadline. Only that stream becomes quiet.
	if _, err = a.Poll(1, 3*time.Second); err != nil {
		t.Fatal("one lost RESET ended the engine", err)
	}
	if _, err = a.Queue(1, 3, []byte("sibling lives")); err != nil {
		t.Fatal(err)
	}
	replies, err := b.Receive(1, reset)
	if err != nil || len(replies) != 1 {
		t.Fatal(replies, err)
	}
	if _, err = a.Receive(1, replies[0]); err != nil {
		t.Fatal("late RESET_ACK", err)
	}
	if !a.aborts[1].done {
		t.Fatal("late peer final was not applied")
	}
	if err = a.Cleaned(1, job.Attempt().ID); err != nil {
		t.Fatal(err)
	}
	if err = a.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestFieldResetRouteSurvivesSocketCloseUntilPeerAck(t *testing.T) {
	a, _ := pair(t)
	_, err := a.Abort(1, 1, wire.ReasonClosed)
	if err != nil {
		t.Fatal(err)
	}
	app, socket := localTCPPair(t)
	defer app.Close()
	retired := 0
	p := &Pump{engine: a, sockets: map[uint64]*socketState{1: {socket: socket, done: make(chan struct{})}}, cfg: PumpConfig{Retired: func(uint64) { retired++ }}}
	p.dropSocket(1)
	if retired != 0 || !p.awaitingRetirement[1] {
		t.Fatal("RESET_ACK route was retired with the socket")
	}
	if _, err = a.Receive(1, control(t, 1, wire.Abort{Ack: true, Code: wire.ReasonClosed})); err != nil {
		t.Fatal(err)
	}
	p.dropSocket(1)
	p.dropSocket(1)
	if retired != 1 || len(p.awaitingRetirement) != 0 {
		t.Fatal("retirement must run once after authenticated ACK", retired)
	}
}

func TestFieldControlSnapshotsBoundBurstAndPreserveLatest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &Pump{ctx: ctx, cfg: PumpConfig{Engine: DefaultConfig(1, []string{"a01"}), ControlQueue: 8}, controls: make(chan wire.Frame, 8), urgentControls: make(chan wire.Frame, 2)}
	p.pendingControls.wake = make(chan struct{}, 1)
	for n := uint64(1); n <= 100; n++ {
		for id := uint64(1); id <= 63; id += 2 {
			if err := p.control(control(t, id, wire.Received{Frontier: n})); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(p.pendingControls.latest) != 32 {
		t.Fatal("obsolete updates were retained")
	}
	if err := p.control(wire.Frame{Type: wire.GrantUsage}); err != nil {
		t.Fatal(err)
	}
	f, ok := p.nextControl()
	if !ok || f.Type != wire.GrantUsage {
		t.Fatal("quota descriptor was starved")
	}
	for n := 0; n < 32; n++ {
		f, ok = p.nextControl()
		if !ok || f.Offset != 100 {
			t.Fatal("latest frontier lost", f)
		}
	}
}
