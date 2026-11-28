package mailstream

import "time"

// Pace measured bulk delivery in MIME-sized bursts. Pacing individual packets
// would leave the APPEND actors publishing many small, expensive containers.
// Initial/unmeasured flights and the independent control carrier are unchanged.
// All state belongs to Session.mu; no additional queue or storage is allocated.
type bulkPacer struct {
	started     time.Time
	bytes       int
	quantumRate float64 // cellular rate at admission, not a retroactive rate
	deferrals   uint64
	guarded     bool
}

func (w *deliveryWindow) pacingRate(now time.Time) float64 {
	if w.mobile.enabled {
		return w.mobile.pacing(now)
	}
	// Keep the qualified startup and slow-provider behavior. Smooth publication
	// only after independent control receipts expose shared-link queueing. Keep
	// that burst smooth until idle; cycling it off mid-transfer refills the queue.
	if !w.adaptive || w.rate <= 0 || w.saturatedBase == 0 {
		return 0
	}
	if !w.pacer.guarded {
		// Once multiple MIME containers fit in a measured flight, a burst
		// of compressed receipts must not release the whole flight at once.
		// Retain headroom to discover bandwidth on a faster path.
		if w.controlBase == 0 || w.rate*w.saturatedBase.Seconds() < 4*maxMailPlain {
			return 0
		}
		return w.rate * 1.30
	}
	gain := 1.25
	if now.Before(w.controlSlowUntil) {
		gain = 1
	}
	return w.rate * gain
}

func (p *bulkPacer) ready(n int, rate float64, now time.Time) bool {
	return p.readyQuantum(n, rate, now, false)
}
func (p *bulkPacer) readyQuantum(n int, rate float64, now time.Time, mobile bool) bool {
	if rate <= 0 {
		p.started, p.bytes = time.Time{}, 0
		return true
	}
	accountedRate := rate
	if mobile && p.quantumRate > 0 && p.bytes > 0 {
		accountedRate = p.quantumRate
	}
	interval := time.Duration(float64(p.bytes) / accountedRate * float64(time.Second))
	if p.started.IsZero() {
		p.started, p.bytes = now, 0
	} else if deadline := p.started.Add(interval); !now.Before(deadline) {
		// Keep the fractional scheduling remainder across dispatcher ticks.
		// Starting each burst at the late wake rounds every interval upward
		// and can systematically depress the measured delivery rate. Bound
		// carry to one tick so idle time cannot accumulate a burst of credit.
		if floor := now.Add(-5 * time.Millisecond); deadline.Before(floor) {
			deadline = floor
		}
		p.started, p.bytes = deadline, 0
	} else if mobile && p.bytes > 0 && p.quantumRate > 0 && rate != p.quantumRate {
		// A burst already admitted at a fast rate must not become seconds
		// of artificial debt when a subsequent receipt lowers the rate.
		// Conversely, do not append a new-rate burst before the old budget
		// has expired. Flight and drain gates still bound real backlog.
		p.deferrals++
		return false
	}
	if mobile && p.bytes == 0 {
		p.quantumRate = rate
	}
	quantum := max(maxMailPlain, min(4*maxMailPlain, int(rate*0.008)))
	if mobile {
		quantum = max(n, min(maxMailPlain, int(rate*0.025)))
	}
	if p.bytes+n <= quantum {
		return true
	}
	p.deferrals++
	return false
}

func (p *bulkPacer) admitted(n int) {
	if !p.started.IsZero() {
		p.bytes += n
	}
}
