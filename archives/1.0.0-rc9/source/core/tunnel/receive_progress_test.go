package tunnel

import (
	"context"
	"fmt"
	"itom.local/core/wire"
	"testing"
)

func TestBulkReceiveProgressKeepsQuietPathsAndCoalescesAcrossQueue(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	for i := 1; i <= 5; i++ {
		l := stripeTestLane(g, fmt.Sprintf("a%02d", i), true)
		defer l.cancel()
		l.controlQueue = make(chan queuedControl, 8)
		l.urgentQueue = make(chan queuedControl, 8)
		l.maintenanceQueue = make(chan queuedControl, 8)
		if e := g.attach(l); e != nil {
			t.Fatal(e)
		}
	}
	owner := g.lanes["a01"]
	if e := g.register(7, owner); e != nil {
		t.Fatal(e)
	}
	g.mu.Lock()
	reserve := g.interactiveReadersLocked()
	g.mu.Unlock()
	for i := 1; i <= 100; i++ {
		f, _ := wire.EncodeControl(7, wire.Progress{Receipt: uint64(128<<10 + i), MaxStream: runtimeStreamWindow + uint64(i), MaxData: runtimeReceiveBytes + uint64(i)})
		if e := g.sendStreamControl(context.Background(), owner, f); e != nil {
			t.Fatal(e)
		}
	}
	total := 0
	var carrier *lane
	for _, l := range g.lanes {
		n := len(l.controlQueue)
		total += n
		if n > 0 {
			carrier = l
		}
		if (l == reserve[0] || l == reserve[1]) && n > 0 {
			t.Fatal("bulk feedback occupied quiet path")
		}
	}
	if total != 1 {
		t.Fatal("feedback fragmented across mailboxes", total)
	}
	f := carrier.resolveFlowControl(<-carrier.controlQueue)
	c, _ := wire.DecodeControl(f.Frame)
	if c.(wire.Progress).Receipt != 128<<10+100 {
		t.Fatal("credit/ACK didn't converge", c)
	}
	g.retire(7, owner)
	g.mu.Lock()
	_, left := g.feedbackAffinity[7]
	g.mu.Unlock()
	if left {
		t.Fatal("retired stream retained affinity")
	}
}

func TestControlBatchProgressPreservesSenderAndReceiverAdvances(t *testing.T) {
	l := &lane{}
	a, _ := wire.EncodeControl(7, wire.Progress{Attempted: 100, Receipt: 20, MaxStream: 200, MaxData: 300})
	b, _ := wire.EncodeControl(7, wire.Progress{Receipt: 90, MaxStream: 260, MaxData: 600})
	batch := l.appendControlBatch([]queuedControl{{Frame: a}}, queuedControl{Frame: b}, 1)
	if len(batch) != 1 {
		t.Fatal("unmerged progress", len(batch))
	}
	c, _ := wire.DecodeControl(batch[0].Frame)
	if c.(wire.Progress).Receipt != 90 || c.(wire.Progress).Attempted != 100 {
		t.Fatal(c)
	}
}
