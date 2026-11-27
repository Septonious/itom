package mailstream

import "time"

// One budget, owned by Session.mu. The original desktop controller remains
// unchanged. Mobile capacity uses compression-safe receipt rate samples.
type mobileFlow struct {
	enabled             bool
	capacity            float64 // acknowledged wire bytes per second
	updated             time.Time
	lastDeferrals       uint64
	lowSamples          int
	guardUntil          time.Time
	gain                float64
	appBase             time.Duration
	idleBase, idleSigma time.Duration
}

const mobileStartRate = 1_000_000

func (m *mobileFlow) reset(enabled bool) {
	*m = mobileFlow{enabled: enabled, capacity: mobileStartRate}
}
func (m *mobileFlow) observeDelay(rtt, base time.Duration, now time.Time, busy bool) {
	if !busy || rtt <= 0 {
		return
	}
	gain := 1.0
	if rtt >= 700*time.Millisecond || base > 0 && rtt-base > 350*time.Millisecond {
		gain = .50
	} else if rtt > 400*time.Millisecond && (base == 0 || rtt-base > 100*time.Millisecond) {
		gain = .82
	}
	if gain < 1 {
		if now.Before(m.guardUntil) {
			gain = min(gain, m.gain)
		}
		m.gain = gain
		m.guardUntil = now.Add(2 * time.Second)
	}
}
func (m *mobileFlow) observeRate(rate float64, now time.Time, limited bool, bulk, base time.Duration) {
	if rate <= 0 || !m.updated.IsZero() && now.Sub(m.updated) < 250*time.Millisecond {
		return
	}
	m.updated = now
	if base > 0 && bulk > base+250*time.Millisecond {
		m.observeDelay(bulk, base, now, true)
	}
	// Do not learn a new ceiling from idle or intentionally throttled throughput.
	if !limited {
		m.lowSamples = 0
		return
	}
	if now.Before(m.guardUntil) {
		// A genuine radio capacity drop is visible even below the guard's
		// commanded rate. Require several samples and independent bulk delay.
		if base > 0 && bulk > base+250*time.Millisecond && rate < m.pacing(now)*.65 {
			m.lowSamples++
		} else {
			m.lowSamples = 0
		}
		if m.lowSamples >= 4 {
			m.capacity = max(64_000, max(rate*1.2/m.gain, m.capacity*.85))
			m.lowSamples = 0
		}
		return
	}
	if rate > m.capacity*.80 {
		m.capacity = min(1.10*m.capacity, max(m.capacity, 1.10*rate))
		m.lowSamples = 0
	} else if rate < m.capacity*.65 && base > 0 && bulk > base+150*time.Millisecond {
		m.lowSamples++
		if m.lowSamples >= 4 {
			m.capacity = max(64_000, max(rate*1.15, m.capacity*.85))
			m.lowSamples = 0
		}
	} else {
		m.lowSamples = 0
	}
}
func (m *mobileFlow) pacing(now time.Time) float64 {
	if now.Before(m.guardUntil) {
		return max(64_000, m.capacity*m.gain)
	}
	return m.capacity * 1.05
}
func (m *mobileFlow) horizon(base, saturated time.Duration) time.Duration {
	return min(900*time.Millisecond, max(200*time.Millisecond, max(base, saturated))+75*time.Millisecond)
}
func (m *mobileFlow) window(base, saturated time.Duration, now time.Time) int {
	// 75ms queue allowance, with conservative bootstrap and bounded baseline.
	baseline := max(base, saturated)
	if baseline <= 0 {
		if now.Before(m.guardUntil) && m.gain <= .5 {
			return maxWire * 2
		}
		return 256 << 10
	}
	horizon := m.horizon(base, saturated)
	gain := 1.0
	if now.Before(m.guardUntil) {
		gain = m.gain
	}
	limit := int(m.capacity * gain * horizon.Seconds())
	// Severe excess delay requires an actual drain, not merely a slower refill.
	// The bound is relaxed automatically after fresh healthy samples.
	if gain <= .5 {
		limit = min(limit, 256<<10)
	}
	return min(maxPendingBytes, max(2*maxWire, limit))
}

// The old peer treats Ping offsets as opaque tokens and echoes them. High
// marker bits advertise the bearer; the low 62 bits remain monotonic.
const bearerMarker uint64 = 1 << 63
const bearerMobile uint64 = 1 << 62
const bearerSeqMask uint64 = (1 << 62) - 1

func bearerPingToken(seq uint64, mobile bool) uint64 {
	token := bearerMarker | (seq & bearerSeqMask)
	if mobile {
		token |= bearerMobile
	}
	return token
}
func (s *Session) setCellularLocked(enabled bool) {
	if s.delivery.mobile.enabled == enabled {
		return
	}
	s.delivery.mobile.reset(enabled)
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
	if sample >= 700*time.Millisecond && s.delivery.bulkFlight == 0 {
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
