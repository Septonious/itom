package datapath

import (
	"bytes"
	"context"
	"errors"
	"io"
	"itom.local/core/flow"
	"itom.local/core/wire"
	"net"
	"testing"
	"time"
)

func pair(t *testing.T) (*Engine, *Engine) {
	t.Helper()
	c := DefaultConfig(1, []string{"a01"})
	a, err := New(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Stop)
	t.Cleanup(b.Stop)
	wa, err := a.Activate(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	wb, err := b.Activate(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Receive(1, wb); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Receive(1, wa); err != nil {
		t.Fatal(err)
	}
	return a, b
}
func control(t *testing.T, stream uint64, m wire.Control) wire.Frame {
	t.Helper()
	f, err := wire.EncodeControl(stream, m)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func TestReorderingShortDeliveryFINAndReceiptBeforeCompletion(t *testing.T) {
	a, b := pair(t)
	var frames []wire.Frame
	var jobs []*Job
	for _, s := range []string{"abc", "def"} {
		id, err := a.Queue(1, 1, []byte(s))
		if err != nil {
			t.Fatal(err)
		}
		f, _ := a.Frame(1, id)
		j, err := a.Begin(1, id, "a01", 1000, 0)
		if err != nil {
			t.Fatal(err)
		}
		frames = append(frames, f)
		jobs = append(jobs, j)
	}
	fin, _ := a.Finish(1, 1)
	acks, err := b.Receive(1, fin)
	if err != nil || b.HalfClosed(1, 1) {
		t.Fatal(err)
	}
	if _, err = a.Receive(1, acks[0]); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Receive(1, frames[1]); err != nil {
		t.Fatal(err)
	}
	got, _ := b.Peek(1, 1)
	if len(got) != 0 {
		t.Fatal("out-of-order delivery")
	}
	receipts, err := b.Receive(1, frames[0])
	if err != nil {
		t.Fatal(err)
	}
	// Receipt can precede delayed APPEND completion: logical bytes disappear,
	// physical attempts remain until verified GC.
	if _, err = a.Receive(1, receipts[0]); err != nil {
		t.Fatal(err)
	}
	if a.AcknowledgedBytes() != 6 {
		t.Fatal("receipt feedback", a.AcknowledgedBytes())
	}
	if _, err = a.Receive(1, receipts[0]); err != nil || a.AcknowledgedBytes() != 6 {
		t.Fatal("duplicate receipt inflated feedback", err, a.AcknowledgedBytes())
	}
	if _, err = a.Receive(1, control(t, 1, wire.Received{Frontier: 7})); err == nil || a.AcknowledgedBytes() != 6 {
		t.Fatal("invalid receipt changed feedback", err, a.AcknowledgedBytes())
	}
	tx, _ := a.Snapshot()
	if tx.Logical != 0 || tx.Attempts != 2 {
		t.Fatal(tx)
	}
	for _, j := range jobs {
		if err = a.Complete(1, j.attempt.ID, flow.CommitUnknown); err != nil {
			t.Fatal(err)
		}
	}
	got, _ = b.Peek(1, 1)
	if string(got) != "abc" {
		t.Fatal(string(got))
	}
	if _, err = b.Delivered(1, 1, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Receive(1, frames[0]); err != nil {
		t.Fatal(err)
	}
	got, _ = b.Peek(1, 1)
	if string(got) != "bc" {
		t.Fatal(string(got))
	}
	_, _ = b.Delivered(1, 1, 2)
	got, _ = b.Peek(1, 1)
	if string(got) != "def" {
		t.Fatal(string(got))
	}
	_, _ = b.Delivered(1, 1, 3)
	if !b.HalfClosed(1, 1) {
		t.Fatal("FIN did not drain")
	}
	reverse, _ := b.Finish(1, 1)
	reply, err := a.Receive(1, reverse)
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.Receive(1, reply[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Retire(1, 1); err != nil {
		t.Fatal(err)
	}
	if err = b.Retire(1, 1); err != nil {
		t.Fatal(err)
	}
	again, err := b.Receive(1, fin)
	if err != nil || len(again) != 1 || again[0].Type != wire.FinAck {
		t.Fatal(again, err)
	}
	if _, err = b.Activate(1, 1); err == nil {
		t.Fatal("resurrected stream")
	}
	for _, j := range jobs {
		if err = a.Cleaned(1, j.attempt.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err = a.Check(); err != nil {
		t.Fatal(err)
	}
	if err = b.Check(); err != nil {
		t.Fatal(err)
	}
}
func TestLostOnlyDataNACKAndBoundedRepairs(t *testing.T) {
	a, b := pair(t)
	id, _ := a.Queue(1, 1, []byte("lost"))
	j, err := a.Begin(1, id, "a01", 1000, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = a.Complete(1, j.attempt.ID, flow.CommitUnknown)
	for n := 1; n <= 3; n++ {
		frames, err := a.Poll(1, time.Duration(n*2)*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if a.RepairReady(id) {
			t.Fatal("timeout authorized repair")
		}
		var nack wire.Frame
		for _, f := range frames {
			reply, err := b.Receive(1, f)
			if err != nil {
				t.Fatal(err)
			}
			if len(reply) > 0 {
				nack = reply[0]
			}
		}
		if nack.Type != wire.Nack {
			t.Fatal("lost tail not discovered")
		}
		if _, err = a.Receive(1, nack); err != nil {
			t.Fatal(err)
		}
		if n == 3 {
			if a.RepairReady(id) {
				t.Fatal("fourth attempt")
			}
			break
		}
		if !a.RepairReady(id) {
			t.Fatal("missing repair")
		}
		j, err = a.Begin(1, id, "a01", 1000, time.Duration(n*2)*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = a.Receive(1, nack); err != nil {
			t.Fatal(err)
		}
		if a.RepairReady(id) {
			t.Fatal("duplicate NACK amplified")
		}
		_ = a.Complete(1, j.attempt.ID, flow.Committed)
	}
	// Third attempt is not an immediate reset: late original may still arrive.
	f, _ := a.Frame(1, id)
	receipt, err := b.Receive(1, f)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Receive(1, receipt[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Poll(1, 121*time.Second); err != nil {
		t.Fatal("receipted DATA expired", err)
	}
}
func checkLostWindowRecoveryAndGenerationStop(t *testing.T) {
	c := DefaultConfig(7, []string{"a01"})
	a, _ := New(context.Background(), c)
	b, _ := New(context.Background(), c)
	defer b.Stop()
	_, _ = a.Activate(7, 1)
	_, _ = b.Activate(7, 1)
	if _, err := a.Queue(7, 1, []byte("x")); !errors.Is(err, flow.ErrCredit) {
		t.Fatal(err)
	}
	frames, err := b.Poll(7, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range frames {
		if _, err = a.Receive(7, f); err != nil {
			t.Fatal(err)
		}
	}
	id, err := a.Queue(7, 1, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	j, err := a.Begin(7, id, "a01", 1000, 0)
	if err != nil {
		t.Fatal(err)
	}
	a.Stop()
	if _, ok := j.claim(); ok {
		t.Fatal("stopped job claimed")
	}
	if err = a.Complete(7, j.attempt.ID, flow.Committed); err != ErrGeneration {
		t.Fatal(err)
	}
	if _, err = b.Receive(6, control(t, 1, wire.Received{})); err != ErrGeneration {
		t.Fatal(err)
	}
}

type shortConn struct{ net.Conn }

func (s shortConn) SetWriteDeadline(time.Time) error { return nil }
func (s shortConn) Write(b []byte) (int, error)      { return min(2, len(b)), io.ErrUnexpectedEOF }
func checkPartialSocketErrorCountIsRetained(t *testing.T) {
	r := WriteSocket(context.Background(), shortConn{}, bytes.Repeat([]byte("a"), 10))
	if r.N != 2 || !errors.Is(r.Err, io.ErrUnexpectedEOF) {
		t.Fatal(r)
	}
}

func TestUnknownCompletionProbesWithoutRefundOrBlindRetry(t *testing.T) {
	a, b := pair(t)
	// Establish the ordinary next periodic probe two seconds into the future.
	if _, err := a.Poll(1, 0); err != nil {
		t.Fatal(err)
	}
	id, err := a.Queue(1, 1, []byte("unknown-before-rejoin"))
	if err != nil {
		t.Fatal(err)
	}
	j, err := a.Begin(1, id, "a01", 1024, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Complete(1, j.attempt.ID, flow.CommitUnknown); err != nil {
		t.Fatal(err)
	}
	before, _ := a.Snapshot()
	if a.RetryReady(id) {
		t.Fatal("unknown authorized blind DATA retry")
	}
	frames, err := a.Poll(1, 110*time.Millisecond)
	if err != nil || len(frames) == 0 {
		t.Fatal("ambiguous writer waited for periodic probe", err)
	}
	after, _ := a.Snapshot()
	if after.Logical != before.Logical || after.AccountDebt["a01"] != before.AccountDebt["a01"] || after.Attempts != before.Attempts {
		t.Fatal("probe refunded debt", before, after)
	}
	// The authenticated receiver's response can authorize the missing byte range.
	for _, f := range frames {
		out, err := b.Receive(1, f)
		if err != nil {
			t.Fatal(err)
		}
		for _, reply := range out {
			if _, err = a.Receive(1, reply); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !a.RepairReady(id) {
		t.Fatal("authenticated missing-range response did not authorize repair")
	}
	// A second unknown completion within the same interval cannot force another
	// early status burst, even when several logical jobs share one failed APPEND.
	repair, err := a.Begin(1, id, "a01", 1024, 120*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Complete(1, repair.attempt.ID, flow.CommitUnknown); err != nil {
		t.Fatal(err)
	}
	frames, err = a.Poll(1, 130*time.Millisecond)
	if err != nil || len(frames) != 0 {
		t.Fatal("failed batch forced unbounded status bursts", len(frames), err)
	}
}

// Resource-independent checks form one scenario; lifecycle and concurrency
// checks in this file remain independently scheduled.
func TestScenarioEngine(t *testing.T) {
	t.Log("check=checkLostWindowRecoveryAndGenerationStop")
	checkLostWindowRecoveryAndGenerationStop(t)
	t.Log("check=checkPartialSocketErrorCountIsRetained")
	checkPartialSocketErrorCountIsRetained(t)
}
