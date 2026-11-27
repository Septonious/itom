package mailstream

import (
	"testing"
	"time"
)

func TestRC11MobileBudgetAndGuard(t *testing.T) {
	now := time.Now()
	w := deliveryWindow{adaptive: true}
	w.mobile.reset(true)
	if got := w.allowance(); got != 256<<10 {
		t.Fatal(got)
	}
	for i := 0; i < 20; i++ {
		w.mobile.observeRate(5_342_000, now.Add(time.Duration(i)*250*time.Millisecond), true, 550*time.Millisecond, 550*time.Millisecond)
	}
	if w.mobile.capacity > 6_800_000 || w.mobile.capacity < 2_000_000 {
		t.Fatal(w.mobile.capacity)
	}
	w.mobile.capacity = 2_150_000
	w.base = 550 * time.Millisecond
	if got := w.allowance(); got < 1_340_000 || got > 1_350_000 {
		t.Fatal(got)
	}
	w.mobile.observeDelay(1277*time.Millisecond, 179*time.Millisecond, now, true)
	if got := w.allowance(); got > 810_000 {
		t.Fatal(got)
	}
	if got := w.pacingRate(now); got != 1_075_000 {
		t.Fatal(got)
	}
	w.mobile.observeRate(200_000, now.Add(6*time.Second), true, 550*time.Millisecond, 550*time.Millisecond)
	if w.pacingRate(now.Add(7*time.Second)) <= 0 {
		t.Fatal("no recovery")
	}
	var p bulkPacer
	if !p.readyQuantum(maxWire, 1_000_000, now, true) {
		t.Fatal("one packet must fit")
	}
	p.admitted(maxWire)
	if p.readyQuantum(maxWire, 1_000_000, now, true) {
		t.Fatal("burst too large")
	}
}
func TestRC11BearerTokenMonotonic(t *testing.T) {
	s := &Session{server: true, changed: make(chan struct{}), wake: make(chan struct{}, 1)}
	s.observePeerBearer(bearerPingToken(2, true))
	if !s.delivery.mobile.enabled {
		t.Fatal("no mobile mode")
	}
	s.observePeerBearer(bearerPingToken(1, false))
	if !s.delivery.mobile.enabled {
		t.Fatal("stale token")
	}
	s.observePeerBearer(bearerPingToken(3, false))
	if s.delivery.mobile.enabled {
		t.Fatal("no desktop reset")
	}
	s.observePeerBearer(4)
	if s.delivery.mobile.enabled {
		t.Fatal("legacy ping changed mode")
	}
}
func TestRC11AccountArrayCapacity(t *testing.T) {
	l := &IMAPLink{reclaimer: NewReclamationCoordinator()}
	for i := 0; i < MaxAccounts; i++ {
		l.accounts = append(l.accounts, &mailAccountState{config: MailAccount{ID: string(rune('a' + i))}})
	}
	if s := l.Snapshot(); s.AllocatedAccounts != 30 || len(s.Lines) != 30 {
		t.Fatal(s.AllocatedAccounts, len(s.Lines))
	}
}

// Deterministic shared serialization queue with a 17 -> 3 -> 17 Mbps
// bearer change. Not a live network/throughput qualification gate.
func TestRC11MobileVariableRateModel(t *testing.T) {
	type flight struct {
		sent, ack time.Time
		mark      deliveryMark
	}
	const quantum = 48 << 10
	w := deliveryWindow{adaptive: true}
	w.mobile.reset(true)
	start := time.Unix(1000, 0)
	finish := start
	var pending []flight
	var bytes [3]int
	var maxQueue [3]time.Duration
	var maxWindow [3]int
	for tick := 0; tick < 12000; tick++ {
		now := start.Add(time.Duration(tick) * 5 * time.Millisecond)
		phase := tick / 4000
		rate := 17_200_000
		if phase == 1 {
			rate = 3_000_000
		}
		for len(pending) > 0 && !now.Before(pending[0].ack) {
			p := pending[0]
			pending = pending[1:]
			w.delivered(quantum, now.Sub(p.sent), now, true, p.mark)
			bytes[phase] += quantum
		}
		queue := max(0, finish.Sub(now))
		maxQueue[phase] = max(maxQueue[phase], queue)
		if tick%50 == 0 {
			w.observeInteractive(180*time.Millisecond+queue, now)
		}
		for range 16 {
			if w.flight+quantum > w.allowanceAt(now) {
				w.limited = true
				break
			}
			if !w.pacer.readyQuantum(quantum, w.pacingRate(now), now, true) {
				break
			}
			w.pacer.admitted(quantum)
			finish = maxTime(finish, now).Add(time.Duration(quantum*8) * time.Second / time.Duration(rate))
			pending = append(pending, flight{now, finish.Add(500 * time.Millisecond), w.sent(quantum, now, true)})
		}
		maxWindow[phase] = max(maxWindow[phase], w.allowanceAt(now))
	}
	for i := range 3 {
		t.Logf("phase=%d delivery=%.2f Mbps max serialization queue=%s max flight budget=%d", i, float64(bytes[i])*8/20e6, maxQueue[i], maxWindow[i])
		if bytes[i] == 0 || maxWindow[i] > 8<<20 {
			t.Fatal("unbounded or stalled", i)
		}
	}
}
