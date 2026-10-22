package tunnel

import (
	"context"
	"testing"
	"time"
)

func TestBoundedResponseSearchDoesNotAddIdleTurnsDuringHotRequest(t *testing.T) {
	p := responseWaitPolicy{boundedPoll: true}
	for _, direct := range []time.Duration{time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond, 180 * time.Millisecond, time.Second} {
		if !p.enabled(direct, true) || p.enabled(direct, false) {
			t.Fatal("search requires measured independent service", direct)
		}
		for i := 0; i < 16; i++ {
			if p.empty(true, false, 0) {
				t.Fatal("pending response acquired an additional IDLE turn", direct, i)
			}
		}
		if d := p.poll(direct); d < 2*time.Millisecond || d > 20*time.Millisecond {
			t.Fatal("unbounded empty command pressure", direct, d)
		}
	}
	if p.enabled(0, true) || p.enabled(-time.Millisecond, true) {
		t.Fatal("unknown timing cannot enable search")
	}
	if p.empty(false, false, 0) {
		t.Fatal("hot policy outlived request window")
	}
}

func TestBoundedResponseSearchKeepsNoPushProviderLegacyBound(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := &noPushResponseMailbox{}
	l := &lane{ctx: ctx, mail: m, pages: make(chan pageResult, 224), readWake: make(chan struct{}, 1)}
	l.active.Store(1)
	l.armReaderHot(time.Second)
	l.startReader()
	defer func() { cancel(); l.workers.Wait() }()
	time.Sleep(600 * time.Millisecond)
	if polls := m.polls.Load(); polls > 14 {
		t.Fatal("unsupported notification route acquired fast polling", polls)
	}
}

func TestBoundedResponseNewBulkEpochKeepsCapacityWithoutOldPlateau(t *testing.T) {
	m := throughputModel{retainWindow: true, bandwidth: 200, fullRate: 200, lowRates: 2, dropReference: 200}
	now := time.Unix(100, 0)
	seed := m.next(now, modelFeedback{deliveryFeedback: deliveryFeedback{Window: 16 << 20}})
	if seed != deliveryInitialWindow || m.bandwidth != 200 || m.fullRate != 0 || m.lowRates != 0 {
		t.Fatal("drained epoch retained old startup/drop evidence or erased capacity", m)
	}
	for i := 0; i < 3; i++ {
		at := now.Add(time.Duration(i+1) * time.Second)
		seed = m.next(at, modelFeedback{deliveryFeedback: deliveryFeedback{Window: seed, Flight: seed, ACKFlight: seed, Demand: true, Delivered: uint64(i+1) << 20, RateMbps: float64(uint64(10) << i)}, RateAt: at, RTTAt: at, RTT: 100 * time.Millisecond})
	}
	if !m.startup || seed <= deliveryInitialWindow {
		t.Fatal("old fast epoch prematurely ended fresh bandwidth discovery", m, seed)
	}
}
