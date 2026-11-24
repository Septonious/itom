package mailstream

import "time"

// Pace measured bulk delivery in MIME-sized bursts. Pacing individual packets
// would leave the APPEND actors publishing many small, expensive containers.
// Initial/unmeasured flights and the independent control carrier are unchanged.
// All state belongs to Session.mu; no additional queue or storage is allocated.
type bulkPacer struct {
	started   time.Time
	bytes     int
	deferrals uint64
	guarded   bool
}

func (w *deliveryWindow) pacingRate(now time.Time) float64 {
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
	if rate <= 0 {
		p.started, p.bytes = time.Time{}, 0
		return true
	}
	interval := time.Duration(float64(p.bytes) / rate * float64(time.Second))
	if p.started.IsZero() || !now.Before(p.started.Add(interval)) {
		p.started, p.bytes = now, 0
	}
	quantum := max(maxMailPlain, min(4*maxMailPlain, int(rate*0.008)))
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
