package mailstream

import (
	"encoding/binary"
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
	// The new receipt-driven probe must discover a higher rate with stable
	// delivery RTT; a historical 300ms floor must not trap a 600ms round.
	w.mobile.beginRecovery(now)
	for i := 1; i <= 16; i++ {
		at := now.Add(time.Duration(i) * time.Second)
		w.mobile.observeRate(w.mobile.pacing(at)*.95, at, true, 600*time.Millisecond, 300*time.Millisecond)
	}
	if got := w.pacingRate(now.Add(17 * time.Second)); got < 2_000_000 || got > 12_500_000 {
		t.Fatal("recovery did not probe", got)
	}
	w.mobile.reset(true)
	w.mobile.capacity = 2_150_000
	w.base = 550 * time.Millisecond
	w.mobile.observeDelay(1277*time.Millisecond, 179*time.Millisecond, now, true)
	if got := w.pacingRate(now); got != 1_075_000 {
		t.Fatal(got)
	}
	if got := w.allowanceAt(now); got > 810_000 {
		t.Fatal(got)
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
	client := &Session{pings: map[uint64]chan struct{}{}}
	id := bearerPingToken(7, true) | bearerDemand
	client.pings[id] = make(chan struct{})
	data := make([]byte, 17)
	binary.BigEndian.PutUint64(data, 261000)
	binary.BigEndian.PutUint64(data[8:], 123456)
	if err := client.apply(frame{kind: framePong, offset: id, data: data}); err != nil {
		t.Fatal(err)
	}
	if snap := client.Snapshot(); !snap.PeerDemandFresh || snap.PeerBulkPending != 261000 || snap.PeerBulkDelivered != 123456 {
		t.Fatal(snap)
	}
	// An older or unsolicited reply cannot replace the active estimate.
	id = bearerPingToken(6, true) | bearerDemand
	client.pings[id] = make(chan struct{})
	binary.BigEndian.PutUint64(data, 1)
	if err := client.apply(frame{kind: framePong, offset: id, data: data}); err != nil {
		t.Fatal(err)
	}
	if client.Snapshot().PeerBulkPending != 261000 {
		t.Fatal("stale feedback applied")
	}
	// RC13 echoes an empty pong, preserving backward compatibility.
	id = bearerPingToken(8, true) | bearerDemand
	client.pings[id] = make(chan struct{})
	if err := client.apply(frame{kind: framePong, offset: id}); err != nil {
		t.Fatal(err)
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

func TestRC12MobileStallGuardsNextBurstWithoutInventingCapacity(t *testing.T) {
	s := &Session{}
	s.delivery.mobile.reset(true)
	initial := s.delivery.mobile.capacity
	s.ObserveApplicationRTT(2 * time.Second)
	m := &s.delivery.mobile
	if m.capacity != initial || m.gain != .5 || !time.Now().Before(m.guardUntil) {
		t.Fatal("missing conservative guard", m.capacity, m.gain)
	}
	if got := s.delivery.allowance(); got != 2*maxWire {
		t.Fatal("initial burst not bounded", got)
	}
	if got := m.pacing(m.guardUntil.Add(time.Second)); got != initial*1.05 {
		t.Fatal("guard did not expire", got)
	}
}

func TestRC13MobileDrainWithoutReceipts(t *testing.T) {
	now := time.Unix(1000, 0)
	m := mobileFlow{}
	m.reset(true)
	c := m.capacity
	m.progress(now, 0, 750000)
	m.progress(now.Add(3*time.Second), 0, 750000)
	if !m.draining || m.capacity != c || m.pacing(now.Add(3*time.Second)) > 64000 {
		t.Fatal("did not drain", m)
	}
	if !m.permitBulkRetry(now.Add(3 * time.Second)) {
		t.Fatal("first retry denied")
	}
	m.noteBulkRetry(now.Add(3 * time.Second))
	if m.permitBulkRetry(now.Add(4 * time.Second)) {
		t.Fatal("retry burst")
	}
	m.progress(now.Add(9*time.Second), 0, 750000)
	if !m.draining {
		t.Fatal("timer-only recovery")
	}
	m.progress(now.Add(10*time.Second), 750000, 0)
	m.observeDelay(250*time.Millisecond, 200*time.Millisecond, now.Add(11*time.Second), false)
	m.observeDelay(250*time.Millisecond, 200*time.Millisecond, now.Add(12*time.Second), false)
	if m.draining || m.recoveryGain != .1 {
		t.Fatal("recovery", m)
	}
	if got := m.pacing(now.Add(time.Minute)); got > c*.11 {
		t.Fatal("expired guard caused burst", got)
	}
	for i := 0; i < 10; i++ {
		m.observeRate(c, now.Add(time.Duration(i+13)*time.Second), true, 250*time.Millisecond, 200*time.Millisecond)
	}
	if m.pacing(now.Add(23*time.Second)) < c {
		t.Fatal("healthy progress failed to restore capacity", m.pacing(now.Add(23*time.Second)))
	}
}

// Sender-local controllers operate against distinct serialization queues. The
// reverse path charges receipt/control traffic; an outage delays bytes rather
// than falsely acknowledging them. This is a synthetic delivery-clock model,
// not a real provider throughput measurement.
func TestRC14AsymmetricRecovery(t *testing.T) {
	type f struct {
		sent, ack   time.Time
		mark        deliveryMark
		receiptSent bool
	}
	type lane struct {
		w       deliveryWindow
		pending []f
		bytes   [5]int
	}
	t.Run("rambler-shared-receipts", func(t *testing.T) {
		lanes := [2]lane{}
		for i := range lanes {
			lanes[i].w.adaptive = true
			lanes[i].w.mobile.reset(true)
		}
		start := time.Unix(1000, 0)
		next := [2]time.Time{start, start}
		rates := [5][2]int{{30_000_000, 30_000_000}, {49_100_000, 4_520_000}, {2_500_000, 1_500_000}, {0, 0}, {49_100_000, 4_520_000}}
		ends := [5]int{12, 32, 44, 47, 87}
		phaseAt := func(at time.Time) int {
			for i, end := range ends {
				if at.Before(start.Add(time.Duration(end) * time.Second)) {
					return i
				}
			}
			return 4
		}
		charge := func(i, n int, now time.Time) time.Time {
			at := maxTime(next[i], now)
			bits := int64(n * 8)
			for bits > 0 {
				p := phaseAt(at)
				r := int64(rates[p][i])
				boundary := start.Add(time.Duration(ends[p]) * time.Second)
				if p == 4 {
					at = at.Add(time.Duration(bits * int64(time.Second) / r))
					break
				}
				if r == 0 {
					at = boundary
					continue
				}
				dt := time.Duration(bits * int64(time.Second) / r)
				if !at.Add(dt).After(boundary) {
					at = at.Add(dt)
					break
				}
				bits -= int64(boundary.Sub(at)) * r / int64(time.Second)
				at = boundary
			}
			next[i] = at
			return at
		}
		const quantum = 48 << 10
		for tick := 0; tick < ends[4]*200; tick++ {
			now := start.Add(time.Duration(tick) * 5 * time.Millisecond)
			phase := phaseAt(now)
			for i := range lanes {
				l := &lanes[i]
				w := &l.w
				for j := range l.pending {
					p := &l.pending[j]
					if !p.receiptSent && !now.Before(p.ack) {
						p.ack = charge(1-i, 512, now).Add(330 * time.Millisecond)
						p.receiptSent = true
					}
				}
				for len(l.pending) > 0 && l.pending[0].receiptSent && !now.Before(l.pending[0].ack) {
					p := l.pending[0]
					l.pending = l.pending[1:]
					w.delivered(quantum, now.Sub(p.sent), now, true, p.mark)
					l.bytes[phase] += quantum
				}
				w.mobile.progress(now, w.totalDelivered, w.bulkFlight)
				if tick%100 == 0 && phase != 3 {
					w.observeInteractive(160*time.Millisecond+max(0, next[0].Sub(now), next[1].Sub(now)), now)
				}
			}
			for i := range lanes {
				l := &lanes[i]
				w := &l.w
				for range 8 {
					if w.mobile.draining || w.flight+quantum > w.allowanceAt(now) {
						w.limited = true
						break
					}
					if !w.pacer.readyQuantum(quantum, w.pacingRate(now), now, true) {
						break
					}
					w.pacer.admitted(quantum)
					end := charge(i, quantum, now)
					l.pending = append(l.pending, f{now, end.Add(170 * time.Millisecond), w.sent(quantum, now, true), false})
				}
				if w.allowanceAt(now) > 8<<20 {
					t.Fatal("unbounded window")
				}
			}
		}
		for i, l := range lanes {
			for p, n := range l.bytes {
				t.Logf("direction=%d phase=%d bytes=%d", i, p, n)
			}
			if l.bytes[1] == 0 || l.bytes[2] == 0 || l.bytes[4] == 0 {
				t.Fatal("no progress", i, l.bytes)
			}
			if l.bytes[4]/40 < l.bytes[2]/12 {
				t.Fatal("did not recover", i, l.bytes)
			}
		}
	})
}

// Continuous sparse flight with receipt gaps longer than the old idle
// threshold must still train the rate estimator.
func TestSparseMobileFlightRetainsReceiptClock(t *testing.T) {
	now := time.Unix(1000, 0)
	w := deliveryWindow{adaptive: true}
	w.mobile.reset(true)
	w.mobile.probeRate = 100_000
	w.base = 100 * time.Millisecond
	for i := range 8 {
		mark := w.sent(48<<10, now, true)
		w.limited = true
		now = now.Add(1100 * time.Millisecond)
		w.delivered(48<<10, 1100*time.Millisecond, now, true, mark)
		if i > 0 && w.mobile.updated.IsZero() {
			t.Fatal("continuous flight lost delivery rate estimator")
		}
	}
	if w.rate <= 0 || w.mobile.lastRate <= 0 {
		t.Fatal("continuous sparse flight failed to measure delivered bytes", w.rate, w.mobile.lastRate)
	}
}

func TestMobileReverseDemandReservesControlHeadroom(t *testing.T) {
	now := time.Unix(1000, 0)
	m := mobileFlow{}
	m.reset(true)
	m.probeRate = 500_000
	m.lastRate = 500_000
	m.controlDelay = 100 * time.Millisecond
	m.observeDelay(300*time.Millisecond, 100*time.Millisecond, now, true)
	if m.probeRate != 500_000 {
		t.Fatal("undemanded reservation", m.probeRate)
	}
	m.reverseDemand = true
	m.observeDelay(300*time.Millisecond, 100*time.Millisecond, now.Add(time.Second), true)
	if m.probeRate != 440_000 {
		t.Fatal("reverse demand did not reserve headroom", m.probeRate)
	}
	m.observeRate(440_000, now.Add(2*time.Second), true, 250*time.Millisecond, 100*time.Millisecond)
	if m.probeRate != 440_000 {
		t.Fatal("hold did not prevent immediate refill", m.probeRate)
	}
	m.observeRate(440_000, now.Add(5*time.Second), true, 250*time.Millisecond, 100*time.Millisecond)
	if m.probeRate <= 440_000 {
		t.Fatal("healthy delivery did not resume probing", m.probeRate)
	}
}

func TestMobileDrainTicksPreserveReceiptEvidence(t *testing.T) {
	now := time.Unix(1000, 0)
	m := mobileFlow{}
	m.reset(true)
	m.progress(now, 0, 4*maxWire)
	m.progress(now.Add(3*time.Second), 0, 4*maxWire)
	m.progress(now.Add(4*time.Second), uint64(maxWire), 3*maxWire)
	m.progress(now.Add(8*time.Second), uint64(maxWire), 3*maxWire)
	if !m.draining || m.recoverySamples != 1 {
		t.Fatal("drain tick discarded receipt evidence", m)
	}
	m.progress(now.Add(9*time.Second), uint64(2*maxWire), 3*maxWire)
	m.progress(now.Add(10*time.Second), uint64(3*maxWire), 2*maxWire)
	if m.draining {
		t.Fatal("drained flight did not recover", m)
	}
}

func TestMobileWindowAllowsBoundedLongRound(t *testing.T) {
	now := time.Unix(1000, 0)
	m := mobileFlow{}
	m.reset(true)
	m.probeRate = 1_000_000
	m.lastBulk = 700 * time.Millisecond
	if got := m.window(100*time.Millisecond, 100*time.Millisecond, now); got != 675_000 {
		t.Fatal("bounded round allowance", got)
	}
	m.draining = true
	if got := m.window(100*time.Millisecond, 100*time.Millisecond, now); got != 2*maxWire {
		t.Fatal("drain allowance", got)
	}
}

func TestMobileFastRecoveryRequiresHealthyReceiptRound(t *testing.T) {
	now := time.Unix(1000, 0)
	for _, tc := range []struct {
		name    string
		bulk    time.Duration
		reverse bool
		rate    float64
		want    float64
	}{
		{"healthy", 180 * time.Millisecond, false, 100_000, 180_000},
		{"queued", 500 * time.Millisecond, false, 100_000, 135_000},
		{"reverse", 180 * time.Millisecond, true, 100_000, 135_000},
		{"underutilized", 180 * time.Millisecond, false, 70_000, 105_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := mobileFlow{}
			m.reset(true)
			m.probeRate = 100_000
			m.reverseDemand = tc.reverse
			m.observeRate(tc.rate, now, true, tc.bulk, 100*time.Millisecond)
			if got := m.pacing(now); got < tc.want-1 || got > tc.want+1 {
				t.Fatal("unexpected recovery rate", got, tc.want)
			}
		})
	}
}

func TestMobileApplicationStallDoesNotClearDrainEvidence(t *testing.T) {
	s := &Session{}
	s.delivery.mobile.reset(true)
	m := &s.delivery.mobile
	m.draining = true
	m.progressAt = time.Now().Add(-5 * time.Second)
	m.recoverySamples = 1
	s.delivery.bulkFlight = maxWire
	s.ObserveApplicationRTT(2 * time.Second)
	if m.recoverySamples != 1 {
		t.Fatal("lost existing drain evidence", m.recoverySamples)
	}
}

func TestPeerDownloadDemandExcludesCreditOnlyTail(t *testing.T) {
	s := &Session{bulkOutstanding: 2 << 20}
	for _, tc := range []struct {
		pending           int
		outstanding, want uint64
	}{
		{0, 2 << 20, 0}, {64, 2 << 20, 64}, {256 << 10, 2 << 20, 256 << 10}, {512 << 10, 0, 0}, {512 << 10, 128 << 10, 128 << 10},
	} {
		s.pendingBytes = tc.pending
		s.bulkOutstanding = tc.outstanding
		if got := s.peerDownloadDemandLocked(); got != tc.want {
			t.Fatal("incorrect peer demand", tc, got)
		}
	}
}
