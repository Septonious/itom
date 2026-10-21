package datapath

import (
	"context"
	"testing"
	"time"

	"itom.local/core/flow"
	"itom.local/core/wire"
)

func TestRev4ShortRecoveryNeedsNACKAndDoesNotAccelerateBulk(t *testing.T) {
	cfg := DefaultConfig(1, []string{"a01"})
	cfg.DeliveryGrace = 1750 * time.Millisecond
	cfg.InteractiveDeliveryGrace = 250 * time.Millisecond
	cfg.InteractiveProbeInterval = 400 * time.Millisecond
	a, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Stop()
	b, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Stop()
	for _, id := range []uint64{1, 3} {
		if _, err = a.Activate(1, id); err != nil {
			t.Fatal(err)
		}
		credit, err := b.Activate(1, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = a.Receive(1, credit); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = a.Poll(1, 0); err != nil {
		t.Fatal(err)
	}
	items := make([]uint64, 2)
	for i, size := range []int{4096, 32 << 10} {
		items[i], err = a.Queue(1, uint64(1+i*2), make([]byte, size))
		if err != nil {
			t.Fatal(err)
		}
		job, err := a.Begin(1, items[i], "a01", uint64(size*2), 0)
		if err != nil {
			t.Fatal(err)
		}
		if err = a.Complete(1, job.Attempt().ID, flow.Committed); err != nil {
			t.Fatal(err)
		}
	}
	frames, err := a.Poll(1, 400*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if a.RepairReady(items[0]) || a.RepairReady(items[1]) {
		t.Fatal("timer authorized a DATA copy")
	}
	shortStatus := false
	for _, frame := range frames {
		if frame.Stream == 3 {
			t.Fatal("short poll accelerated bulk recovery")
		}
		shortStatus = shortStatus || frame.Stream == 1
		replies, err := b.Receive(1, frame)
		if err != nil {
			t.Fatal(err)
		}
		for _, reply := range replies {
			if _, err = a.Receive(1, reply); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !shortStatus || !a.RepairReady(items[0]) {
		t.Fatal("short DATA did not discover authenticated missing range promptly")
	}
	bulkNACK, _ := wire.EncodeControl(3, wire.Missing{ID: 1, Ranges: []wire.ByteRange{{Start: 0, End: 32 << 10}}})
	if _, err = a.Receive(1, bulkNACK); err != nil {
		t.Fatal(err)
	}
	if a.RepairReady(items[1]) {
		t.Fatal("bulk visibility grace was bypassed")
	}
	if a.tx.LogicalBytes() != 36<<10 {
		t.Fatal("recovery timer or NACK refunded logical bytes")
	}
}
