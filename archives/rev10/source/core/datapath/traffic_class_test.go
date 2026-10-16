package datapath

import (
	"itom.local/core/wire"
	"testing"
)

// Fragmentation, delayed receipts and an unrelated short stream share one
// owner. Class changes affect scheduling only, never credit or byte offsets.
func TestTrafficClassFragmentedBurstAndAuthenticatedDrain(t *testing.T) {
	a, b := pair(t)
	for i := 0; i < 16; i++ {
		id, err := a.Queue(1, 1, make([]byte, 4<<10))
		if err != nil {
			t.Fatal(err)
		}
		job, err := a.Begin(1, id, "a01", 6000, 0)
		if err != nil {
			t.Fatal(err)
		}
		if job.Interactive() != (i < 15) || job.Interactive() != a.ItemInteractive(id, 4<<10) {
			t.Fatal("fragment class disagrees", i)
		}
	}
	window, err := b.Activate(1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Activate(1, 3); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Receive(1, window); err != nil {
		t.Fatal(err)
	}
	small, err := a.Queue(1, 3, []byte("request"))
	if err != nil || !a.ItemInteractive(small, 7) {
		t.Fatal("bulk affected another stream", err)
	}
	if _, err = a.Receive(1, control(t, 1, wire.Received{Frontier: 64<<10 + 1})); err == nil || !a.streams[1].bulk {
		t.Fatal("invalid receipt changed class", err)
	}
	if _, err = a.Receive(1, control(t, 1, wire.Received{Frontier: 64 << 10})); err != nil {
		t.Fatal(err)
	}
	if a.streams[1].bulk || a.AcknowledgedBytes() != 64<<10 {
		t.Fatal("authenticated drain not observed")
	}
	next, err := a.Queue(1, 1, []byte("next request"))
	if err != nil || !a.ItemInteractive(next, 12) {
		t.Fatal("drained stream lost interactive reserve", err)
	}
	job, err := a.Begin(1, next, "a01", 1000, 0)
	if err != nil || !job.Interactive() {
		t.Fatal("handoff class differs after drain", err)
	}
}
