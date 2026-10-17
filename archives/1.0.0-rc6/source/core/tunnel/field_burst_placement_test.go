package tunnel

import (
	"fmt"
	"sort"
	"testing"
	"time"

	"itom.local/core/wire"
)

// RC6 client trace: 291 of 464 fast DATA writes on one account even though
// ten carriers were connected. Instantaneous ACK debt loses the rapid history.
func TestFieldParallelStartupDoesNotConcentrateShortWrites(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	for i := 1; i <= 10; i++ {
		l := stripeTestLane(g, fmt.Sprintf("a%02d", i), false)
		defer l.cancel()
		l.rtt.Store(420)
		if err := g.attach(l); err != nil { t.Fatal(err) }
	}
	counts := map[string]int{}
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	for i := 0; i < 160; i++ {
		keys := make([]string, 0, 10)
		for id := range g.lanes { keys = append(keys, id) }
		sort.Strings(keys)
		g.prioritizeFastTargetsLocked(keys, nil)
		g.preferShortTargetLocked(keys, g.lanes["a03"])
		winner := keys[0]
		counts[winner]++
		g.recordFastPlacementLocked(winner, now)
		g.turn++
	}
	if counts["a03"] > 56 {
		t.Fatalf("field parallel startup monopolized physical carrier: %v", counts)
	}
	participating := 0
	for _, n := range counts { if n > 0 { participating++ } }
	if participating < 7 { t.Fatalf("short burst did not use available carriers: %v", counts) }
}

func TestFastPlacementHistoryIsIdleSafeAndAdvisory(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	now := time.Now()
	for i := 0; i < 41; i++ { g.recordFastPlacementLocked("a03", now) }
	if p := g.fastPlacementPenaltyLocked("a03", now); p <= 0 || p > 1024 {
		t.Fatalf("unbounded advisory penalty %d", p)
	}
	idle := now.Add(1100*time.Millisecond)
	if p := g.fastPlacementPenaltyLocked("a03", idle); p != 0 {
		t.Fatalf("idle account retained old placement penalty=%d", p)
	}
	g.recordFastPlacementLocked("a03", idle)
	if p := g.fastPlacementPenaltyLocked("a03", idle); p != 0 {
		t.Fatalf("next cold request did not reset burst memory=%d", p)
	}
	if g.delivery.bulk != 0 || g.delivery.fast != 0 || g.delivery.window != deliveryInitialWindow {
		t.Fatal("advisory scheduling changed authenticated logical credit")
	}
}
