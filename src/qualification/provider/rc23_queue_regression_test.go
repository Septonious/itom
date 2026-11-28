package main

import (
	"context"
	"net"
	"testing"
	"time"
)

// Model bookkeeping must represent actual pending bytes. Closing an abandoned
// shaped connection must return its queue charges exactly once, even when its
// underlying peer stops reading. This is a fixture invariant, not a field bug.
func TestRC23ShaperCancellationReleasesQueueCharges(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, b := net.Pipe()
	defer b.Close()
	n := &network{start: time.Now(), bitsPerSecond: 100_000, rtts: []time.Duration{time.Millisecond}, step: time.Second}
	c := shape(ctx, a, n)
	defer c.Close()
	if got, err := c.Write(make([]byte, 3*16<<10)); err != nil || got != 3*16<<10 {
		t.Fatalf("write %d %v", got, err)
	}
	if n.queued.Load() == 0 {
		t.Fatal("fixture did not queue bytes")
	}
	cancel()
	_ = c.Close()
	deadline := time.Now().Add(time.Second)
	for n.queued.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if q := n.queued.Load(); q != 0 {
		t.Fatalf("closed connection retained %d queued bytes", q)
	}
}

func TestShaperCloseUnblocksFullQueue(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	n := &network{start: time.Now(), bitsPerSecond: 1000, rtts: []time.Duration{time.Millisecond}, step: time.Second}
	c := shape(context.Background(), a, n)
	done := make(chan error, 1)
	go func() { _, err := c.Write(make([]byte, 64*16<<10)); done <- err }()
	deadline := time.Now().Add(time.Second)
	for n.queued.Load() < 32*16<<10 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if n.queued.Load() < 32*16<<10 {
		c.Close()
		t.Fatal("queue did not fill")
	}
	closed := make(chan struct{})
	go func() { c.Close(); close(closed) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("blocked write unexpectedly succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("close did not interrupt writer")
	}
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("close did not join pumps")
	}
	if n.queued.Load() != 0 || c.pending.Load() != 0 || n.abandoned.Load() == 0 {
		t.Fatalf("bad accounting: queue=%d pending=%d abandoned=%d", n.queued.Load(), c.pending.Load(), n.abandoned.Load())
	}
}

func TestShaperDeadlineWhileWaitingForQueue(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	n := &network{start: time.Now(), bitsPerSecond: 1000, rtts: []time.Duration{time.Millisecond}, step: time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := shape(ctx, a, n)
	defer c.Close()
	c.SetWriteDeadline(time.Now().Add(40 * time.Millisecond))
	if _, err := c.Write(make([]byte, 64*16<<10)); err == nil {
		t.Fatal("full queue ignored deadline")
	}
	cancel()
	c.Close()
	if n.queued.Load() != 0 || c.pending.Load() != 0 {
		t.Fatal("deadline leaked reservation")
	}
}
