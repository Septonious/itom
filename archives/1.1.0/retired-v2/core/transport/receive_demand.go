package transport

import "time"

// ReceiveDemand observes actual application consumption, not APPEND, inventory
// echoes or advertised speed. It requests prompt bounded growth for sustained
// demand; it is not a bandwidth estimate or permission to reserve memory. The
// session/lease owner must still enforce shared backing and delivery evidence.
type ReceiveDemand struct {
	start, last time.Time
	bytes       uint64
}

func (d *ReceiveDemand) Consume(now time.Time, n, window, ceiling, lightRate uint64) uint64 {
	if n == 0 || window == 0 || window > ceiling || ceiling > 1<<30 || n > ceiling || lightRate == 0 || lightRate > 1<<30 {
		return window
	}
	if window == ceiling {
		return window
	}
	horizon := max(time.Nanosecond, time.Duration(float64(window)/float64(lightRate)*float64(time.Second)))
	if d.start.IsZero() || now.Before(d.last) || now.Sub(d.start) >= horizon || now.Sub(d.last) >= horizon {
		d.start = now
		d.bytes = 0
	}
	d.last = now
	d.bytes += n
	if d.bytes < max(uint64(1), window/2) {
		return window
	}
	elapsed := now.Sub(d.start)
	if elapsed > 0 && float64(d.bytes)/elapsed.Seconds() < float64(lightRate) {
		return window
	}
	d.bytes = 0
	d.start = now
	return min(ceiling, window*2)
}
