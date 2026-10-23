package tunnel

import (
	"fmt"
	"itom.local/core/wire"
	"testing"
)

// Different login completion orders must converge to the same receive path.
// Keeping an old mixed-layout hysteresis choice defeats request priming even
// though both endpoints now have the same complete, healthy account set.
func TestDEV9SplitReserveConvergesAcrossStartupOrderAndBulk(t *testing.T) {
	for _, loaded := range []bool{false, true} {
		for _, first := range []string{"a03", "a06"} {
			t.Run(fmt.Sprintf("%s-loaded%t", first, loaded), func(t *testing.T) {
				g := newStripeGroup(wire.ID{1}, 1)
				for i := 1; i <= 6; i++ {
					l := stripeTestLane(g, fmt.Sprintf("a%02d", i), false)
					t.Cleanup(l.cancel)
					l.dataShards = 1
					if e := g.attach(l); e != nil {
						t.Fatal(e)
					}
				}
				g.interactiveReserve = [2]string{first, "a04"}
				if loaded {
					g.delivery.bulk = 1 << 20
				}
				g.mu.Lock()
				r := g.interactiveReadersLocked()
				g.mu.Unlock()
				if r[0] == nil || r[0].account.ID != "a01" || r[1] != nil {
					t.Fatalf("split receive path depends on startup history: first=%s reserve=%v", first, g.interactiveReserve)
				}
				g.mu.Lock()
				keys := []string{"a02", "a01", "a03", "a04", "a05", "a06"}
				g.prioritizeFastTargetsLocked(keys, nil)
				g.preferShortTargetLocked(keys, g.lanes[first])
				g.mu.Unlock()
				if keys[0] != "a01" {
					t.Fatalf("old startup affinity displaced the protected primed path: %v", keys)
				}
			})
		}
	}
}
