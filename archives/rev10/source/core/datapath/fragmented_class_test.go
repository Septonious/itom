package datapath

import (
	"itom.local/core/flow"
	"itom.local/core/wire"
	"testing"
	"time"
)

func TestMTUStreamsBecomeBulkEvenWhenEveryFragmentIsAcknowledged(t *testing.T) {
	a, b := pair(t)
	for stream := uint64(3); stream < 48; stream += 2 {
		credit, err := b.Activate(1, stream)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = a.Activate(1, stream); err != nil {
			t.Fatal(err)
		}
		if _, err = a.Receive(1, credit); err != nil {
			t.Fatal(err)
		}
	}
	for turn := 0; turn < 64; turn++ {
		if err := a.clock(time.Duration(turn) * 10 * time.Millisecond); err != nil {
			t.Fatal(err)
		}
		for stream := uint64(1); stream < 48; stream += 2 {
			id, err := a.Queue(1, stream, make([]byte, 1460))
			if err != nil {
				t.Fatal(err)
			}
			job, err := a.Begin(1, id, "a01", 2000, a.now)
			if err != nil {
				t.Fatal(err)
			}
			if turn >= 45 && job.Interactive() {
				t.Fatal("sustained MTU stream kept fast reserve", stream, turn)
			}
			if err = a.Complete(1, job.Attempt().ID, flow.Committed); err != nil {
				t.Fatal(err)
			}
			if _, err = a.Receive(1, control(t, stream, wire.Received{Frontier: uint64(turn+1) * 1460})); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := a.clock(4 * time.Second); err != nil {
		t.Fatal(err)
	}
	id, err := a.Queue(1, 1, make([]byte, 1460))
	if err != nil || !a.ItemInteractive(id, 1460) {
		t.Fatal("idle exchange lost fast classification", err)
	}
}
