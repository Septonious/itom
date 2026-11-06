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
}

func (w *deliveryWindow) pacingRate(now time.Time) float64 {
	if !w.adaptive || w.rate <= 0 || w.saturatedBase == 0 ||
		w.bulkFlight == 0 && now.Sub(w.last) > max(time.Second, 4*w.base) {
		return 0
	}
	gain := 1.25
	if now.Before(w.controlSlowUntil) {
		gain = 1 // Stop probing while the existing shared-link guard drains.
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
	// Let a fast path fill several APPEND actors in parallel. The quantum is
	// at most 50 ms of the measured rate, with one MIME container as its floor
	// and four as its ceiling; slower links therefore retain bounded bursts.
	quantum := max(maxMailPlain, min(4*maxMailPlain, int(rate*0.05)))
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
