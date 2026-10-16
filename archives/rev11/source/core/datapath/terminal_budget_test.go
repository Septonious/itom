package datapath

import (
	"itom.local/core/flow"
	"itom.local/core/wire"
	"testing"
	"time"
)

func TestTerminalRetriesAreBoundedAndLateAckPreservesDebt(t *testing.T) {
	a, b := pair(t)
	a.cfg.IsolateStreamTimeouts = true
	item, err := a.Queue(1, 1, []byte("physical debt"))
	if err != nil {
		t.Fatal(err)
	}
	j, err := a.Begin(1, item, "a01", 512, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Complete(1, j.Attempt().ID, flow.CommitUnknown); err != nil {
		t.Fatal(err)
	}
	first, err := a.Abort(1, 1, wire.ReasonTimeout)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := a.Snapshot()
	count := 1
	for i := 1; i <= 70; i++ {
		frames, err := a.Poll(1, time.Duration(i)*2*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range frames {
			if f.Type == wire.Reset {
				count++
			}
		}
	}
	if count > 8 || count < 2 {
		t.Fatalf("unbounded or absent terminal recovery: %d", count)
	}
	after, _ := a.Snapshot()
	if before.PhysicalExposure != after.PhysicalExposure {
		t.Fatal("quiet terminal refunded physical debt")
	}
	reply, err := b.Receive(1, first)
	if err != nil || len(reply) != 1 {
		t.Fatal(reply, err)
	}
	if _, err = a.Receive(1, reply[0]); err != nil || !a.aborts[1].done {
		t.Fatal("late acknowledgement rejected", err)
	}
}

func TestIdleStreamEmitsNoPeriodicStatus(t *testing.T) {
	a, _ := pair(t)
	for i := 1; i <= 30; i++ {
		frames, err := a.Poll(1, time.Duration(i)*2*time.Second)
		if err != nil || len(frames) != 0 {
			t.Fatalf("idle command pressure: %d %v", len(frames), err)
		}
	}
}

func TestIdleReceiverRecoversLostInitialCreditOnDemand(t *testing.T) {
	a, b := pair(t)
	credit, err := a.Activate(1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Activate(1, 3); err != nil {
		t.Fatal(err)
	} // Lose B's initial WINDOW.
	if _, err = b.Receive(1, credit); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Queue(1, 3, []byte("queued without credit")); err == nil {
		t.Fatal("lost WINDOW was not reproduced")
	}
	frames, err := a.Poll(1, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range frames {
		replies, err := b.Receive(1, f)
		if err != nil {
			t.Fatal(err)
		}
		for _, reply := range replies {
			if _, err = a.Receive(1, reply); err != nil {
				t.Fatal(err)
			}
		}
	}
	item, err := a.Queue(1, 3, []byte("queued without credit"))
	if err != nil {
		t.Fatal("missing credit after query", err)
	}
	if _, err = a.Begin(1, item, "a01", 512, 2*time.Second); err != nil {
		t.Fatal("idle receiver did not restore lost credit", err)
	}
}
