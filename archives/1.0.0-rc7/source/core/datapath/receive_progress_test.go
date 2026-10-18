package datapath

import (
	"bytes"
	"itom.local/core/flow"
	"itom.local/core/wire"
	"testing"
)

func TestCombinedReceiveProgressReordersAndRetainsPhysicalDebt(t *testing.T) {
	a, b := pair(t)
	b.cfg.CombinedReceiveProgress = true
	var frames []wire.Frame
	var jobs []*Job
	for _, part := range [][]byte{bytes.Repeat([]byte{1}, 512), bytes.Repeat([]byte{2}, 512)} {
		id, e := a.Queue(1, 1, part)
		if e != nil {
			t.Fatal(e)
		}
		f, _ := a.Frame(1, id)
		j, e := a.Begin(1, id, "a01", 1000, 0)
		if e != nil {
			t.Fatal(e)
		}
		frames = append(frames, f)
		jobs = append(jobs, j)
	}
	out, e := b.Receive(1, frames[1])
	if e != nil || len(out) != 1 {
		t.Fatal(out, e)
	}
	c, _ := wire.DecodeControl(out[0])
	if c.(wire.Progress).Receipt != 0 || c.(wire.Progress).Attempted != 0 {
		t.Fatal("reordered DATA acknowledged", c)
	}
	out, e = b.Receive(1, frames[0])
	if e != nil {
		t.Fatal(e)
	}
	ack := out[0]
	if _, e = a.Receive(1, ack); e != nil || a.AcknowledgedBytes() != 1024 {
		t.Fatal(e, a.AcknowledgedBytes())
	}
	got, e := b.Peek(1, 1)
	if e != nil || len(got) != 512 {
		t.Fatal(e, len(got))
	}
	// Only actual successful socket writes advance credit. The receipt merely
	// frees logical bytes: both physical attempts stay charged through Unknown.
	credit, e := b.Delivered(1, 1, 128)
	if e != nil {
		t.Fatal(e)
	}
	c, _ = wire.DecodeControl(credit)
	if p := c.(wire.Progress); p.Receipt != 1024 || p.MaxStream != b.cfg.StreamWindow+128 || p.Attempted != 0 {
		t.Fatal(p)
	}
	if _, e = a.Receive(1, credit); e != nil {
		t.Fatal(e)
	}
	if _, e = a.Receive(1, ack); e != nil || a.AcknowledgedBytes() != 1024 {
		t.Fatal("delayed ACK regressed receipt", e)
	}
	for _, j := range jobs {
		if e = a.Complete(1, j.attempt.ID, flow.CommitUnknown); e != nil {
			t.Fatal(e)
		}
	}
	tx, _ := a.Snapshot()
	if tx.Logical != 0 || tx.Attempts != 2 {
		t.Fatal("receipt refunded physical debt", tx)
	}
	// A receipt-only snapshot cannot claim opposite-direction attempted DATA,
	// hence cannot synthesize NACKs for DATA not yet visible on another carrier.
	if out, e = a.Receive(1, credit); e != nil || len(out) != 0 {
		t.Fatal("receiver feedback triggered repair", out, e)
	}
}

func TestPendingProgressMergesNewReceiptWithOlderSenderFrontier(t *testing.T) {
	p := &Pump{cfg: PumpConfig{Engine: DefaultConfig(1, []string{"a01"})}, controls: make(chan wire.Frame, 8)}
	a, _ := wire.EncodeControl(1, wire.Progress{Attempted: 100, Receipt: 20, MaxStream: 200, MaxData: 300})
	b, _ := wire.EncodeControl(1, wire.Progress{Receipt: 90, MaxStream: 260, MaxData: 600})
	if e := p.queueSnapshot(a); e != nil {
		t.Fatal(e)
	}
	if e := p.queueSnapshot(b); e != nil {
		t.Fatal(e)
	}
	f, ok := p.takeSnapshot()
	c, _ := wire.DecodeControl(f)
	if !ok || c.(wire.Progress).Attempted != 100 || c.(wire.Progress).Receipt != 90 || c.(wire.Progress).MaxData != 600 {
		t.Fatal(f, c)
	}
	if _, ok = p.takeSnapshot(); ok {
		t.Fatal("two records for one snapshot")
	}
}
