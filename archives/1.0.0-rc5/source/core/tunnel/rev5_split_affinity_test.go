package tunnel

import (
	"itom.local/core/wire"
	"testing"
)

// Split BODY service has an independent short/control mailbox and slot. Bulk
// occupancy must not displace the reader already primed by the request, while
// actual short queue debt must still cause a responsive sibling to be chosen.
func TestRev5SplitBulkDoesNotDisplacePrimedShortReader(t *testing.T) {
	g := newStripeGroup(wire.ID{31}, 1)
	for _, id := range []string{"a01", "a02", "a03"} {
		l := stripeTestLane(g, id, true)
		defer l.cancel()
		l.dataShards = 1
		if err := g.attach(l); err != nil {
			t.Fatal(err)
		}
	}
	preferred := g.lanes["a03"]
	preferred.physicalSlots <- struct{}{}
	preferred.unreceivedLocalRecords.Store(32)
	preferred.unreceivedLocalBytes.Store(1 << 20)
	g.mu.Lock()
	defer g.mu.Unlock()
	keys := []string{"a01", "a02", "a03"}
	g.prioritizeFastTargetsLocked(keys, nil)
	g.preferShortTargetLocked(keys, preferred)
	if keys[0] != "a03" {
		t.Fatal("independent bulk BODY stole primed short return path", keys)
	}
	preferred.urgentQueue = make(chan queuedControl, 8)
	for range 8 {
		preferred.urgentQueue <- queuedControl{}
	}
	g.prioritizeFastTargetsLocked(keys, nil)
	g.preferShortTargetLocked(keys, preferred)
	if keys[0] == "a03" {
		t.Fatal("short/control debt was ignored")
	}
}
