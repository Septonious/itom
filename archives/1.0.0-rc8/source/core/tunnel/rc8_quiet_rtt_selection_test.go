package tunnel

import (
    "testing"
    "time"

    "itom.local/core/wire"
)

// A stale idle echo can be 583ms while another zero-stream account recently
// measured 165ms. Quiet placement must react to the current healthy lane.
func TestRC8FreshQuietEchoOverridesOlderIdleBaseline(t *testing.T) {
    g := newStripeGroup(wire.ID{1}, 1)
    fast := stripeTestLane(g, "a01", false)
    slow := stripeTestLane(g, "a02", false)
    defer fast.cancel()
    defer slow.cancel()
    if err := g.attach(fast); err != nil { t.Fatal(err) }
    if err := g.attach(slow); err != nil { t.Fatal(err) }

    now := time.Now().UnixMilli()
    for _, l := range []*lane{fast, slow} {
        l.idleRTTAt.Store(now - 20000)
        l.idleRTT.Store(583)
        l.rttAt.Store(now)
    }
    fast.rtt.Store(165)
    fast.smoothedRTT.Store(165)
    slow.rtt.Store(583)
    slow.smoothedRTT.Store(583)

    g.mu.Lock()
    got := g.carrierLatencyLocked(fast)
    keys := []string{"a01", "a02"}
    g.prioritizeFastTargetsLocked(keys, nil)
    g.mu.Unlock()
    if got != 165 || keys[0] != "a01" {
        t.Fatalf("fresh quiet 165ms lane lost to stale baseline: latency=%d order=%v", got, keys)
    }

    // Once application traffic is admitted, a queued echo is not a new
    // unloaded baseline: use the retained proven idle sample instead.
    g.active.Store(1)
    g.mu.Lock()
    loaded := g.carrierLatencyLocked(fast)
    g.mu.Unlock()
    if loaded != 583 {
        t.Fatalf("loaded echo replaced idle baseline: %d", loaded)
    }

    // Stale recent-echo data must never mask a still-valid idle baseline.
    g.active.Store(0)
    fast.rttAt.Store(now - 11000)
    g.mu.Lock()
    stale := g.carrierLatencyLocked(fast)
    g.mu.Unlock()
    if stale != 583 {
        t.Fatalf("stale quiet sample replaced idle baseline: %d", stale)
    }

    // Physical outstanding writes mean a nominally zero-stream sample
    // can still be queue-inflated. Preserve the prior idle baseline.
    fast.rttAt.Store(now)
    fast.physicalActive.Store(1)
    g.mu.Lock()
    physicallyBusy := g.carrierLatencyLocked(fast)
    g.mu.Unlock()
    if physicallyBusy != 583 {
        t.Fatalf("physically busy sample replaced idle baseline: %d", physicallyBusy)
    }
}
