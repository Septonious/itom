package mailstream

import (
	"testing"
	"time"
)

func TestRC116InteractiveSelectionPrefersFastUnqueuedAccount(t *testing.T) {
	l := &IMAPLink{}
	var allowed [20]bool
	for i, ms := range []int{20, 30, 100} {
		p := &mailPipe{}
		p.latency.Store(int64(time.Duration(ms) * time.Millisecond))
		p.latencyAt.Store(time.Now().UnixNano())
		a := &mailAccountState{control: p, ready: true}
		a.appendLatency.Store(int64(time.Duration(ms) * time.Millisecond))
		l.accounts = append(l.accounts, a)
		allowed[i] = true
	}
	l.accounts[0].controlQueued = 4
	order := l.interactiveAccountOrder(0, allowed, nil, true)
	if order[0] != 1 || order[1] != 0 || order[2] != 2 {
		t.Fatalf("unexpected queue-aware order: %v", order)
	}
	bulk := l.interactiveAccountOrder(2, allowed, nil, false)
	if bulk[0] != 2 || bulk[1] != 0 || bulk[2] != 1 {
		t.Fatalf("bulk placement changed: %v", bulk)
	}
}

func TestRC116InteractiveSelectionPreservesStableTiesAndExclusion(t *testing.T) {
	l := &IMAPLink{}
	var allowed [20]bool
	for i := 0; i < 3; i++ {
		l.accounts = append(l.accounts, &mailAccountState{ready: true})
		allowed[i] = true
	}
	order := l.interactiveAccountOrder(1, allowed, nil, true)
	if order[0] != 1 || order[1] != 2 || order[2] != 0 {
		t.Fatalf("unknown estimates must preserve round robin: %v", order)
	}
	order = l.interactiveAccountOrder(1, allowed, l.accounts[1], true)
	if order[0] == 1 {
		t.Fatalf("hedge chose excluded account: %v", order)
	}
}

func TestRC116EmptyFetchSamplesWithoutSeparateProbe(t *testing.T) {
	p := &mailPipe{}
	p.observeEmptyControlFetch(time.Now().Add(-20*time.Millisecond))
	if p.latency.Load() < int64(10*time.Millisecond) || p.latencyAt.Load() == 0 {
		t.Fatalf("empty FETCH not measured: %s", time.Duration(p.latency.Load()))
	}
	previous := p.latency.Load()
	p.observeEmptyControlFetch(time.Now().Add(-30*time.Millisecond))
	if p.latency.Load() <= previous {
		t.Fatal("empty FETCH EWMA failed to update")
	}
}
