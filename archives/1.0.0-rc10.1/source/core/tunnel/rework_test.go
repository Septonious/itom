package tunnel

import (
	"context"
	"itom.local/core/wire"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLatencyFirstPolicySpeedtestAndPostLoadRecovery(t *testing.T) {
	var p deliveryPolicy
	now := time.Unix(1000, 0)
	window := uint64(deliveryInitialWindow)
	delivered := uint64(0)
	rng := rand.New(rand.NewSource(714))
	for i := 0; i < 20; i++ {
		delivered += 12_500_000
		window = p.next(now.Add(time.Duration(i)*time.Second), deliveryFeedback{Window: window, Flight: window, Demand: true, Delivered: delivered, RateMbps: 100, Age: time.Duration(350+rng.Intn(70)) * time.Millisecond, FastAge: 250 * time.Millisecond, Samples: 24, Target: deliveryTargetAge})
	}
	if window <= 3<<20 || window > deliveryMaxWindow {
		t.Fatal("healthy high-rate delivery cannot explore beyond the old ceiling", window)
	}
	before := window
	window = p.next(now.Add(21*time.Second), deliveryFeedback{Window: window, Flight: window, Demand: true, Delivered: delivered, RateMbps: 1, Age: 1500 * time.Millisecond, FastAge: 1200 * time.Millisecond, Pressure: true, Target: deliveryTargetAge})
	if window >= before || p.mode != deliveryCongested {
		t.Fatal("latency did not override speed", window, p.mode)
	}
	retained := min(window, uint64(deliveryInitialWindow))
	for i := 22; i < 322; i++ {
		window = p.next(now.Add(time.Duration(i)*time.Second), deliveryFeedback{Window: window, Delivered: delivered})
	}
	if window != retained || p.mode != deliveryIdle {
		t.Fatal("idle did not retain the bounded next-burst budget")
	}
	for i := 322; i < 330; i++ {
		delivered += 12_500_000
		window = p.next(now.Add(time.Duration(i)*time.Second), deliveryFeedback{Window: window, Flight: window, Demand: true, Delivered: delivered, RateMbps: 100, Age: 350 * time.Millisecond, FastAge: 250 * time.Millisecond, Samples: 24, Target: deliveryTargetAge})
	}
	if window <= retained {
		t.Fatal("permanent post-load collapse", window, retained)
	}
}
func TestPolicyOutageAndAppLimitedEstimate(t *testing.T) {
	var p deliveryPolicy
	now := time.Unix(1000, 0)
	f := deliveryFeedback{Window: 1 << 20, Flight: 1 << 20, Demand: true, Delivered: 10_000_000, RateMbps: 80, Age: 300 * time.Millisecond, Samples: 24, Target: deliveryTargetAge}
	p.next(now, f)
	capacity := p.capacityMbps
	f.Flight = 1
	f.RateMbps = .1
	f.Samples = 1
	f.Delivered++
	p.next(now.Add(time.Second), f)
	if p.capacityMbps != capacity {
		t.Fatal("app-limited sample ratcheted capacity", p.capacityMbps)
	}
	f.Age = 4 * time.Second
	f.Samples = 0
	if p.next(now.Add(5*time.Second), f) >= f.Window || p.mode != deliveryOutage {
		t.Fatal("no-progress outage did not bound new admission")
	}
	// Each direction owns its own policy; a slow UL cannot rewrite DL history.
	var other deliveryPolicy
	other.next(now, deliveryFeedback{Window: 1 << 20})
	if p.capacityMbps != capacity {
		t.Fatal("direction estimates were coupled")
	}
}
func TestCapacityBroadcastWakesAllSpeedtestWaiters(t *testing.T) {
	var n capacityNotification
	const workers = 48
	started := make(chan struct{}, workers)
	done := make(chan struct{}, workers)
	for i := 0; i < workers; i++ {
		go func() { wake := n.watch(); started <- struct{}{}; <-wake; done <- struct{}{} }()
	}
	for i := 0; i < workers; i++ {
		<-started
	}
	n.signal()
	timeout := time.After(time.Second)
	for i := 0; i < workers; i++ {
		select {
		case <-done:
		case <-timeout:
			t.Fatal("one-token capacity wake stranded senders")
		}
	}
	select {
	case <-n.watch():
		t.Fatal("new generation inherited stale wake")
	default:
	}
}
func TestSharedAccountAdmissionUnderSpeedtestLoad(t *testing.T) {
	var b accountWorkBudget
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	var active, peak atomic.Int32
	var completed [workClassCount]atomic.Int32
	for i := 0; i < 48; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			class := workData
			if i%8 == 0 {
				class = workUrgent
			} else if i%8 == 1 {
				class = workReceive
			} else if i%8 == 2 {
				class = workMaintenance
			} else if i%8 == 3 {
				class = workDataReceive
			}
			for j := 0; j < 8; j++ {
				release, err := b.acquire(ctx, class)
				if err != nil {
					t.Error(err)
					return
				}
				n := active.Add(1)
				for n > peak.Load() {
					old := peak.Load()
					if n <= old || peak.CompareAndSwap(old, n) {
						break
					}
				}
				time.Sleep(time.Millisecond)
				active.Add(-1)
				completed[class].Add(1)
				release()
				release()
			}
		}(i)
	}
	wg.Wait()
	if peak.Load() > 7 {
		t.Fatal("provider-wide operation cap exceeded", peak.Load())
	}
	for i := range completed {
		if completed[i].Load() == 0 {
			t.Fatal("starved class", i)
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range b.busy {
		if b.busy[i] != 0 || len(b.queues[i]) != 0 {
			t.Fatal("leaked admission", i)
		}
	}
}
func TestControlAdvisoryStormDoesNotSuppressFreshStateOrDurableUsage(t *testing.T) {
	var c controlRepeat
	now := time.Unix(1000, 0)
	f := wire.Frame{Type: wire.Nack, Stream: 3, Offset: 100, Payload: []byte("same hole")}
	if c.duplicate(f, now) {
		t.Fatal("first repair suppressed")
	}
	for i := 0; i < 10000; i++ {
		if !c.duplicate(f, now.Add(time.Millisecond)) {
			t.Fatal("duplicate repair escaped budget")
		}
	}
	changed := f
	changed.Offset++
	if c.duplicate(changed, now) {
		t.Fatal("new evidence suppressed")
	}
	if c.duplicate(f, now.Add(250*time.Millisecond)) {
		t.Fatal("bounded retry suppressed forever")
	}
	f.Type = wire.GrantUsage
	for i := 0; i < 100; i++ {
		if c.duplicate(f, now) {
			t.Fatal("durable publication descriptor suppressed")
		}
	}
}
func TestApplicationByteClockExcludesWarmupAndLateBytes(t *testing.T) {
	now := time.Unix(1000, 0)
	w := newApplicationByteClock(now, 30*time.Second, time.Second)
	w.add(true, 1000000, now.Add(-time.Nanosecond))
	w.add(false, 1000000, now.Add(30*time.Second))
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for sec := 0; sec < 30; sec++ {
				w.add(true, 1000, now.Add(time.Duration(sec)*time.Second))
				w.add(false, 2000, now.Add(time.Duration(sec)*time.Second))
			}
		}()
	}
	wg.Wait()
	up, down := w.totals(30)
	if up != 24*30*1000 || down != 24*30*2000 {
		t.Fatal("incorrect verified application-byte totals", up, down)
	}
	up, down = w.totals(20)
	if up != 24*20*1000 || down != 24*20*2000 {
		t.Fatal("tail sampling included bytes from future windows")
	}
}

func TestInteractivePlacementRotatesEqualHealthyCarriers(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	a, b := stripeTestLane(g, "a01", false), stripeTestLane(g, "a02", false)
	defer a.cancel()
	defer b.cancel()
	g.lanes[a.account.ID] = a
	g.lanes[b.account.ID] = b
	g.mu.Lock()
	defer g.mu.Unlock()
	keys := []string{"a01", "a02"}
	g.turn = 0
	g.prioritizeFastTargetsLocked(keys, nil)
	if keys[0] != "a01" {
		t.Fatal("first fair tie", keys)
	}
	keys = []string{"a01", "a02"}
	g.turn = 1
	g.prioritizeFastTargetsLocked(keys, nil)
	if keys[0] != "a02" {
		t.Fatal("equal fast traffic pinned to first account", keys)
	}
}
