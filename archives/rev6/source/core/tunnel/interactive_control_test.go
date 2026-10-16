package tunnel

import (
	"context"
	"fmt"
	"itom.local/core/wire"
	"testing"
)

func TestLogicalCreditAvoidsPendingBulkAppend(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	var lanes []*lane
	for i := 1; i <= 5; i++ {
		l := stripeTestLane(g, fmt.Sprintf("a%02d", i), false)
		defer l.cancel()
		l.controlQueue = make(chan queuedControl, 8)
		l.urgentQueue = make(chan queuedControl, 8)
		l.maintenanceQueue = make(chan queuedControl, 8)
		if err := g.attach(l); err != nil {
			t.Fatal(err)
		}
		lanes = append(lanes, l)
	}
	owner := lanes[4]
	owner.physicalSlots <- struct{}{}
	f, err := wire.EncodeControl(1, wire.Credit{MaxStream: 12345, MaxData: 12345})
	if err != nil {
		t.Fatal(err)
	}
	if err = g.sendStreamControl(context.Background(), owner, f); err != nil {
		t.Fatal(err)
	}
	if len(owner.controlQueue) != 0 {
		t.Fatal("credit queued behind pending physical APPEND")
	}
	queued := 0
	for _, l := range lanes[:4] {
		queued += len(l.controlQueue)
	}
	if queued != 1 {
		t.Fatal("credit lost or duplicated", queued)
	}
}
