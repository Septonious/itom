package tunnel

import (
	"context"
	"errors"
	"itom.local/core/wire"
	"net"
	"testing"
	"time"
)

// Cancel while the successful target dial's OPEN_OK is still in provider mail.
// The original implementation tombstoned the route and dropped the late reply,
// leaving the peer TCP allocation live rather than issuing its terminal FIN.
func TestCanceledOpenLateReplyReleasesPeerCapacity(t *testing.T) {
	t.Setenv("ITOM_LOAD_COMMAND_DELAY_MS", "25")
	t.Setenv("ITOM_LOAD_VISIBILITY_DELAY_MS", "200")
	f := newProductionSupervisorFixtureAccounts(t, 1)
	snapshots := snapshotLoadCarriers(t, f, 1, 1)
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		s, _, err := f.client.Open(ctx, wire.Connect{Host: "127.0.0.1", Port: uint16(f.listener.Addr().(*net.TCPAddr).Port)})
		if err == nil && s != nil {
			s.Close()
		}
		result <- err
	}()
	deadline := time.Now().Add(3 * time.Second)
	for f.targetDials.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("target not dialed before cancellation")
		}
		if err := pause(f.ctx, 5*time.Millisecond); err != nil {
			t.Fatal(err)
		}
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("OPEN completed before planned cancel", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled OPEN did not return")
	}
	f.waitLane(t, "a01", 0)
	f.mu.Lock()
	server := f.serverLanes["a01"]
	f.mu.Unlock()
	deadline = time.Now().Add(5 * time.Second)
	for f.client.stripe.activeStreams() != 0 || server.stripe.activeStreams() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("abandoned OPEN retains client=%d server=%d routes", f.client.stripe.activeStreams(), server.stripe.activeStreams())
		}
		if err := pause(f.ctx, 10*time.Millisecond); err != nil {
			t.Fatal(err)
		}
	}
	s, _, err := f.client.Open(f.ctx, wire.Connect{Host: "127.0.0.1", Port: uint16(f.listener.Addr().(*net.TCPAddr).Port)})
	if err != nil {
		t.Fatal("OPEN after cancelled transaction", err)
	}
	s.Close()
	assertLoadCarriersPreserved(t, f, snapshots)
}
