package tunnel

import (
	"math"
	"time"
)

// throughputModel is an experimental useful-delivery model, not TCP BBR or
// BRUTAL. It controls logical admission only. It cannot issue physical grants,
// refund debt, acknowledge bytes, change receiver credit, or reconnect carriers.
// Each direction owns its own instance under the existing stripe mutex.
type throughputModel struct {
	retainWindow                                               bool // experiment2: bulk high-water, not an RTT queue target
	mode                                                       deliveryMode
	delivered                                                  uint64
	lastProgress, lastGrowth, lastReduction, lastRate, lastRTT time.Time
	lastDrop, lastProbe, probeUntil                            time.Time
	bandwidth                                                  float64
	rates                                                      [10]modelRate
	nextRate                                                   int
	lowRates                                                   int
	dropReference                                              float64
	minRTT, feedbackRTT                                        time.Duration
	minRTTAt                                                   time.Time
	startup                                                    bool
	plateau                                                    int
	fullRate                                                   float64
	probes                                                     uint64
}

type modelRate struct {
	at   time.Time
	mbps float64
}
type modelFeedback struct {
	deliveryFeedback
	RateAt, RTTAt time.Time
	RTT           time.Duration // original authenticated DATA ACK, never a repair/timer
	SupplyLimited bool          // original bulk is waiting for physical publication
}

func (m *throughputModel) next(now time.Time, f modelFeedback) uint64 {
	window := min(uint64(deliveryMaxWindow), max(uint64(deliveryMinWindow), f.Window))
	progress := f.Delivered > m.delivered
	if m.lastProgress.IsZero() || progress {
		m.lastProgress = now
	}
	m.delivered = f.Delivered
	if f.RTT > 0 && f.RTT <= 10*time.Second && f.RTTAt.After(m.lastRTT) {
		m.lastRTT = f.RTTAt
		// A bounded rolling floor is distinct from the current feedback turn.
		// Loaded RTT is allowed to change BDP, never interpreted as packet loss.
		if m.minRTT == 0 || f.RTT < m.minRTT || now.Sub(m.minRTTAt) > 30*time.Second {
			m.minRTT, m.minRTTAt = f.RTT, now
		}
		if m.feedbackRTT == 0 {
			m.feedbackRTT = f.RTT
		} else {
			m.feedbackRTT = (3*m.feedbackRTT + f.RTT) / 4
		}
	}

	// A rate sample is consumed once, not once per 250ms actor tick. Smaller
	// application-limited samples cannot erase known capacity. ACK compression
	// is damped by the caller's >=1s useful-byte interval, not APPEND wire rate.
	utilized := max(f.Flight, f.ACKFlight) >= window/2
	rateUtilized := utilized && !f.SupplyLimited
	validRate := f.RateAt.After(m.lastRate) && f.RateMbps > 0 && f.RateMbps <= 10000 && !math.IsNaN(f.RateMbps) && !math.IsInf(f.RateMbps, 0)
	if validRate {
		m.lastRate = f.RateAt
		if !rateUtilized {
			m.lowRates, m.dropReference = 0, 0
		}
		if rateUtilized || f.RateMbps >= m.bandwidth {
			old := m.bandwidth
			if m.lowRates > 0 {
				old = m.dropReference
			}
			m.rates[m.nextRate] = modelRate{now, f.RateMbps}
			m.nextRate = (m.nextRate + 1) % len(m.rates)
			// Three independent full-window feedback intervals establish a drop;
			// a noisy RTT or one quiet interval does not.
			if rateUtilized && old > 0 && f.RateMbps < .7*old {
				if m.lowRates == 0 {
					m.dropReference = old
				}
				m.lowRates++
			} else {
				m.lowRates = 0
				m.dropReference = 0
			}
			if m.lowRates >= 3 {
				for i := range m.rates {
					if m.rates[i].mbps > old*.7 {
						m.rates[i] = modelRate{}
					}
				}
				m.lowRates = 0
				m.startup = false
				m.lastDrop = now
			}
			m.bandwidth = 0
			for _, r := range m.rates {
				if now.Sub(r.at) <= 10*time.Second {
					m.bandwidth = max(m.bandwidth, r.mbps)
				}
			}
			if rateUtilized {
				if f.RateMbps >= m.fullRate*1.15 {
					m.fullRate, m.plateau = f.RateMbps, 0
				} else {
					m.plateau++
				}
				if m.plateau >= 3 {
					m.startup = false
				}
			}
		}
	}
	if !f.Demand && f.Flight == 0 {
		m.mode = deliveryIdle
		m.lastGrowth, m.lastReduction = time.Time{}, time.Time{}
		m.lastProbe, m.probeUntil = time.Time{}, time.Time{}
		m.lastProgress = time.Time{}
		m.startup, m.plateau = true, 0
		// Drained bulk never delays a short request. Keep bounded capacity
		// history for the next download; do not retain a full bulk flight.
		seed := f.InitialWindow
		if seed == 0 {
			seed = deliveryInitialWindow
		}
		return min(uint64(deliveryInitialWindow), max(uint64(deliveryMinWindow), seed))
	}
	if m.mode == "" || m.mode == deliveryIdle {
		m.mode, m.startup = deliveryMode("BULK_STARTUP"), true
	}
	turn := max(250*time.Millisecond, min(3*time.Second, m.feedbackRTT))
	if f.Flight > 0 && now.Sub(m.lastProgress) >= max(3*time.Second, 3*turn) {
		m.mode = deliveryOutage
		if m.lastReduction.IsZero() || now.Sub(m.lastReduction) >= turn {
			m.lastReduction = now
			return max(uint64(deliveryMinWindow), window/2)
		}
		return window
	}
	if !f.Demand || !progress {
		return window
	}
	if !m.lastGrowth.IsZero() && now.Sub(m.lastGrowth) < turn {
		return window
	}
	m.lastGrowth = now
	if m.mode == deliveryOutage {
		m.startup = true
	}
	if m.startup {
		m.mode = deliveryMode("BULK_STARTUP")
		// ACK-clocked bounded startup is independent of an absolute RTT goal.
		if utilized {
			window += max(uint64(64<<10), window/2)
		}
	} else {
		m.mode = deliveryMode("BULK_CRUISE")
		// Cruise keeps two useful-delivery BDPs plus batch/ACK headroom.
		// There is no 650/800ms bulk age cut. All windows remain hard-bounded.
		rtt := max(100*time.Millisecond, max(m.minRTT, m.feedbackRTT))
		target := uint64(min(float64(deliveryMaxWindow), m.bandwidth*1e6/8*rtt.Seconds()*2+128*1024))
		target = max(uint64(deliveryInitialWindow), target)
		if utilized && (m.lastProbe.IsZero() || now.Sub(m.lastProbe) >= 8*turn) && (m.lastDrop.IsZero() || now.Sub(m.lastDrop) >= 3*turn) {
			// One feedback turn at 1.25 gain, then return to the modeled BDP.
			// Persistent plateau cannot grow a queue on every actor tick.
			m.lastProbe, m.probeUntil = now, now.Add(turn)
			m.probes++
		}
		if now.Before(m.probeUntil) {
			target = min(uint64(deliveryMaxWindow), target+target/4)
		}
		if m.retainWindow && (m.lastDrop.IsZero() || now.Sub(m.lastDrop) >= 3*turn) {
			// Mailbox supply is quantized by grants, batch size and cleanup.
			// A smaller modeled RTT/BDP is not proof that reducing bulk flight
			// can preserve goodput. Retain the demonstrated high-water while
			// useful progress continues; drained epochs still return the seed.
			target = max(target, window)
		}
		if m.lastDrop.Equal(now) && target < window {
			window = target
		} else if target < window {
			window = max(target, window-window/4)
		} else {
			window = min(target, window+max(uint64(64<<10), window/4))
		}
	}
	return min(uint64(deliveryMaxWindow), max(uint64(deliveryMinWindow), window))
}
