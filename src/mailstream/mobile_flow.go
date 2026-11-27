package mailstream

import "time"

// One budget, owned by Session.mu. The original desktop controller remains
// unchanged. Mobile capacity uses compression-safe receipt rate samples.
type mobileFlow struct {
	enabled             bool
	capacity            float64 // acknowledged wire bytes per second
	updated             time.Time
	lastDeferrals       uint64
	guardUntil          time.Time
	gain                float64
	appBase             time.Duration
	draining            bool
	progressBytes       uint64
	progressAt          time.Time
	retryAt             time.Time
	recoverySamples     int
	recoveryGain        float64
	idleBase, idleSigma time.Duration
	lastBulk            time.Duration
	goodRate            float64
	probeRate           float64
	probeAt             time.Time
	badSamples          int
	controlDelay        time.Duration
	controlAt           time.Time
	lastRate            float64
	ratePeak            float64
	ratePeakAt          time.Time
	lastBusy            time.Time
	reverseDemand       bool
	reverseHoldUntil    time.Time
	reverseCap          float64 // temporary reservation, independent of learned rate
}

const mobileStartRate = 1_000_000

func (m *mobileFlow) reset(enabled bool) {
	*m = mobileFlow{enabled: enabled, capacity: mobileStartRate}
}

// Each sender has its own receipt clock and learns its own direction. Do
// not turn a high absolute application RTT into a bulk outage when receipts
// continue to arrive. Only lack of progress enters the hard drain state.
func (m *mobileFlow) observeDelay(rtt, base time.Duration, now time.Time, busy bool) {
	if rtt <= 0 {
		return
	}
	if m.draining && !busy && rtt < 2*time.Second {
		m.recoverySamples++
		if m.recoverySamples >= 2 {
			m.beginRecovery(now)
		}
	}
	if !busy || m.draining {
		return
	}
	if m.probeRate > 0 {
		// Fresh reverse backlog and increased control delay justify bounded uplink reservation.
		if m.reverseDemand && base > 0 && rtt > max(250*time.Millisecond, base+150*time.Millisecond) && !now.Before(m.reverseHoldUntil) && m.probeRate > 64_000 {
			// Reverse traffic needs temporary headroom, not a permanent
			// loss of learned uplink capacity. Corroborated congestion below
			// can still lower the actual probe rate.
			m.reverseCap = max(64_000, m.probeRate*.88)
			m.probeAt = now
			m.reverseHoldUntil = now.Add(2 * time.Second)
		}
		// A rising control queue is a corroborating signal, not an absolute
		// RTT gate. Back off at most once per second during an active probe.
		previous := m.controlDelay
		if previous == 0 {
			previous = base
		}
		stale := !m.progressAt.IsZero() && now.Sub(m.progressAt) >= time.Second
		underDelivery := m.lastRate > 0 && m.lastRate < m.probeRate*.75
		if previous > 0 && rtt > previous+max(250*time.Millisecond, previous/2) && now.Sub(m.controlAt) >= time.Second && (stale || underDelivery || rtt >= 1500*time.Millisecond) {
			m.probeRate = max(16000, m.probeRate*.65)
			m.probeAt, m.controlAt = now, now
		}
		if m.controlDelay == 0 {
			m.controlDelay = rtt
		} else {
			m.controlDelay = (3*m.controlDelay + rtt) / 4
		}
		return
	}
	if rtt >= 700*time.Millisecond || base > 0 && rtt-base > 350*time.Millisecond {
		m.gain = .5
		m.guardUntil = now.Add(2 * time.Second)
	} else if rtt > 400*time.Millisecond && (base == 0 || rtt-base > 100*time.Millisecond) {
		m.gain = .82
		m.guardUntil = now.Add(2 * time.Second)
	}
}

// The rate sample is unique, acknowledged wire bytes per second. A bounded
// upward probe can be made on a stable delivery round even if the absolute
// RTT is higher than an old minimum. Failed probes retreat to the last
// delivered operating point rather than restarting at ten percent.
func (m *mobileFlow) observeRate(rate float64, now time.Time, limited bool, bulk, base time.Duration) {
	if rate <= 0 || !m.updated.IsZero() && now.Sub(m.updated) < 250*time.Millisecond {
		return
	}
	m.updated = now
	if m.ratePeakAt.IsZero() || now.Sub(m.ratePeakAt) >= time.Second {
		m.ratePeak, m.ratePeakAt = rate, now
	} else {
		m.ratePeak = max(m.ratePeak, rate)
	}
	m.lastRate = m.ratePeak
	rate = m.ratePeak
	if m.draining {
		return
	}
	previous := m.lastBulk
	if bulk > 0 {
		if previous == 0 {
			m.lastBulk = bulk
		} else {
			m.lastBulk = (3*previous + bulk) / 4
		}
	}
	if !limited || bulk <= 0 {
		m.badSamples = 0
		return
	}
	budget := m.pacing(now)
	rising := previous > 0 && bulk > previous+max(150*time.Millisecond, previous/3)
	if (rising || bulk > max(base+350*time.Millisecond, 2*base)) && rate < budget*.65 {
		m.badSamples++
	} else {
		m.badSamples = 0
	}
	if m.badSamples >= 2 {
		fallback := max(16000, rate*1.20)
		if m.goodRate > 0 {
			fallback = min(fallback, m.goodRate)
		}
		m.probeRate = min(budget*.8, fallback)
		m.probeRate = max(16000, m.probeRate)
		m.probeAt = now
		m.badSamples = 0
		return
	}
	if rising || now.Before(m.reverseHoldUntil) || !m.probeAt.IsZero() && now.Sub(m.probeAt) < time.Second {
		return
	}
	// If the sender is constrained by the window/pacer, try a modest extra
	// quantum on stable receipt progress. Never jump to the reference radio
	// capacity, and never learn from an idle or deliberately throttled sample.
	if rate >= budget*.70 {
		m.goodRate = max(16000, min(budget, rate*1.20))
	}
	gain := 1.35
	if budget < 500_000 && rate >= budget*.85 && base > 0 && bulk <= base+150*time.Millisecond && !rising && !m.reverseDemand && m.badSamples == 0 {
		gain = 1.8
	}
	candidate := min(budget*gain, max(budget*1.05, rate*gain))
	if rate < budget*.55 {
		// A sparse receipt train is not proof of spare capacity. Lower an
		// obviously oversized probe, but permit a small trial if receipts
		// are consistent and the budget was merely flight-limited.
		candidate = max(16000, min(budget*1.05, max(budget*.85, rate*1.8)))
	}
	if m.reverseDemand && budget < 250_000 && m.badSamples == 0 &&
		m.controlDelay > 0 && m.controlDelay <= max(250*time.Millisecond, base+100*time.Millisecond) &&
		previous > 0 && bulk <= previous+75*time.Millisecond && rate >= budget*.70 {
		candidate = max(candidate, min(250_000, min(budget*1.5, budget+32_000)))
	}
	m.probeRate = min(12_500_000, candidate)
	m.capacity = max(m.capacity, m.goodRate)
	m.probeAt = now
	m.recoveryGain = 0
}
func (m *mobileFlow) pacing(now time.Time) float64 {
	if m.draining {
		return min(64_000, m.capacity*.05)
	}
	if m.probeRate > 0 {
		if m.reverseDemand && now.Before(m.reverseHoldUntil) && m.reverseCap > 0 {
			return min(m.probeRate, m.reverseCap)
		}
		return m.probeRate
	}
	if m.recoveryGain > 0 {
		return max(16_000, m.capacity*m.recoveryGain)
	}
	if now.Before(m.guardUntil) {
		return max(16_000, m.capacity*m.gain)
	}
	return m.capacity * 1.05
}
func (m *mobileFlow) horizon(base, saturated time.Duration) time.Duration {
	return min(900*time.Millisecond, max(200*time.Millisecond, max(base, saturated))+75*time.Millisecond)
}
func (m *mobileFlow) window(base, saturated time.Duration, now time.Time) int {
	if m.draining {
		return 2 * maxWire
	}
	baseline := max(base, saturated)
	if baseline <= 0 && m.lastBulk <= 0 {
		if now.Before(m.guardUntil) && m.gain <= .5 {
			return 2 * maxWire
		}
		return 256 << 10
	}
	// Receipt latency includes provider APPEND/FETCH, not just propagation.
	// Allow a full measured delivery round, bounded to prevent delay from
	// turning into an unlimited backlog. Drain never admits new bulk.
	horizon := m.horizon(base, saturated)
	if m.lastBulk > 0 {
		horizon = max(horizon, min(1500*time.Millisecond, min(baseline+500*time.Millisecond, m.lastBulk)+75*time.Millisecond))
	}
	limit := int(m.pacing(now) * horizon.Seconds())
	return min(8<<20, max(2*maxWire, limit))
}

// The old peer treats Ping offsets as opaque tokens and echoes them. High
// marker bits advertise the bearer; the low 61 bits remain monotonic.
const bearerMarker uint64 = 1 << 63
const bearerMobile uint64 = 1 << 62
const bearerDemand uint64 = 1 << 61
const bearerSeqMask uint64 = (1 << 61) - 1

func bearerPingToken(seq uint64, mobile bool) uint64 {
	token := bearerMarker | (seq & bearerSeqMask)
	if mobile {
		token |= bearerMobile
	}
	return token
}
func (s *Session) setCellularLocked(enabled bool) {
	if link, ok := s.link.(*IMAPLink); ok {
		link.mobileFeedback.Store(enabled)
	}
	if enabled {
		s.bearerAdvertised = true
	}
	if s.delivery.mobile.enabled == enabled {
		return
	}
	s.delivery.mobile.reset(enabled)
	s.mobileBearer.Store(enabled)
	s.delivery.limit = initialDeliveryWindow
	s.delivery.pacer = bulkPacer{}
	s.delivery.saturatedBase = 0
	s.delivery.base = 0
	s.delivery.controlBase = 0
	s.delivery.rate = 0
	s.delivery.ratePeak = 0
	s.delivery.rateSampleMax = 0
	s.notifyLocked()
}
func (s *Session) SetCellular(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setCellularLocked(enabled)
}
func (s *Session) observePeerBearer(token uint64) {
	if !s.server || token&bearerMarker == 0 {
		return
	}
	seq := token & bearerSeqMask
	s.mu.Lock()
	defer s.mu.Unlock()
	if seq <= s.peerBearerSequence {
		return
	}
	s.peerBearerSequence = seq
	s.setCellularLocked(token&bearerMobile != 0)
}
func (s *Session) ObserveApplicationRTT(sample time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := &s.delivery.mobile
	if !m.enabled || sample <= 0 {
		return
	}
	if m.appBase == 0 || sample < m.appBase {
		m.appBase = sample
	}
	baseline := m.appBase
	if m.idleBase > 0 {
		baseline = m.idleBase + min(150*time.Millisecond, 3*m.idleSigma)
	}
	now := time.Now()

	m.observeDelay(sample, baseline, now, s.delivery.bulkFlight > 0)
	// Severe application delay with little/no bulk flight is not a capacity
	// measurement. It does justify a conservative next-burst probe; keep
	// the learned rate untouched and avoid admitting a full startup burst.
	if !m.draining && sample >= 1500*time.Millisecond && s.delivery.bulkFlight > 0 && !m.progressAt.IsZero() && now.Sub(m.progressAt) >= 3*time.Second {
		m.draining = true
		m.recoverySamples = 0
	}
	if sample >= 700*time.Millisecond && s.delivery.bulkFlight == 0 {
		if m.probeRate > 0 {
			m.probeRate = min(m.probeRate, 500_000)
		}
		m.gain = .5
		if until := now.Add(6 * time.Second); until.After(m.guardUntil) {
			m.guardUntil = until
		}
	}
}

func (s *Session) SetMobileBaseline(base, variation time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delivery.mobile.idleBase, s.delivery.mobile.idleSigma = base, variation
}

// Tick even when no delivery sample exists. A lack of receipts is itself
// congestion evidence. Learned capacity survives, but bulk admission stops.
func (m *mobileFlow) beginRecovery(now time.Time) {
	m.draining = false
	m.reverseCap = 0
	m.reverseHoldUntil = time.Time{}
	m.recoverySamples = 0
	m.recoveryGain = .10
	m.probeRate = 0
	if m.goodRate > 0 {
		m.probeRate = max(16_000, m.goodRate*.7)
	}
	m.probeAt = now
	m.lastBulk = 0
	m.controlDelay = 0
	m.badSamples = 0
	m.guardUntil = now.Add(5 * time.Second)
	m.gain = .1
}
func (m *mobileFlow) progress(now time.Time, delivered uint64, flight int) {
	if !m.enabled {
		return
	}
	if m.progressAt.IsZero() {
		m.progressAt = now
		m.progressBytes = delivered
	}
	if delivered != m.progressBytes {
		m.progressBytes = delivered
		m.progressAt = now
		if m.draining {
			m.recoverySamples++
			if m.recoverySamples >= 3 && flight <= 2*maxWire {
				m.beginRecovery(now)
			}
		}
	}
	if flight == 0 {
		m.progressAt = now
		if !m.draining && !m.lastBusy.IsZero() && now.Sub(m.lastBusy) > 5*time.Second && m.probeRate > mobileStartRate {
			// Keep learned goodput, but re-probe after idle: the same cellular
			// bearer may now have very different service capacity.
			m.probeRate = mobileStartRate
			m.probeAt = now
			m.lastBulk = 0
			m.controlDelay = 0
		}
		return
	}
	m.lastBusy = now
	if !m.draining && now.Sub(m.progressAt) >= 3*time.Second {
		m.draining = true
		m.recoverySamples = 0
		m.recoveryGain = .1
		m.probeRate = 0
	}
}
func (m *mobileFlow) permitBulkRetry(now time.Time) bool {
	return !m.draining || !now.Before(m.retryAt)
}
func (m *mobileFlow) noteBulkRetry(now time.Time) {
	if m.draining {
		m.retryAt = now.Add(2 * time.Second)
	}
}
