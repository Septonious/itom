package datapath

import (
	"bytes"
	"context"
	"testing"

	"itom.local/core/flow"
	"itom.local/core/wire"
)

func TestAdaptiveCreditGrowsSingleStreamWithoutAllocatingPerStreamBudgets(t *testing.T) {
	cfg := DefaultConfig(1, []string{"a01"})
	cfg.ReceiveBytes = 32 << 20
	cfg.StreamWindow = 1 << 20
	cfg.MaxStreamWindow = flow.MaxStreamWindow
	e, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	if _, err = e.Activate(1, 1); err != nil {
		t.Fatal(err)
	}
	chunk := bytes.Repeat([]byte{13}, wire.MaxData)
	var limit uint64
	for offset := uint64(0); offset < 12<<20; offset += uint64(len(chunk)) {
		if _, err = e.Receive(1, wire.Frame{Type: wire.Data, Stream: 1, Logical: offset + 1, Offset: offset, Payload: chunk}); err != nil {
			t.Fatal(err)
		}
		f, err := e.Delivered(1, 1, uint64(len(chunk)))
		if err != nil {
			t.Fatal(err)
		}
		c, err := wire.DecodeControl(f)
		if err != nil {
			t.Fatal(err)
		}
		credit := c.(wire.Credit)
		if credit.MaxStream < limit || credit.MaxData != cfg.ReceiveBytes+offset+uint64(len(chunk)) {
			t.Fatal("nonmonotonic or multiplied aggregate credit")
		}
		limit = credit.MaxStream
		if limit-offset-uint64(len(chunk)) > flow.MaxStreamWindow {
			t.Fatal("unbounded single stream permission")
		}
	}
	if limit-(12<<20) <= 4<<20 {
		t.Fatal("single stream retains the old throughput ceiling")
	}
	for id := uint64(3); id < 31; id += 2 {
		if _, err = e.Activate(1, id); err != nil {
			t.Fatal(err)
		}
	}
	if e.rx.Snapshot().MaxData != (32+12)<<20 || e.rx.Snapshot().Buffered != 0 {
		t.Fatal("opening streams multiplied receive allocation")
	}
}
