package tunnel

import "time"

// Stream complete BODYs immediately but amortize command RTT when the tail is
// full. Never raise the protocol's 16-UID bound; slow fetches and empty polls
// shrink the next turn. The IDLE/wake decision remains separate from batching.
type receivePagePolicy struct {
	limit     uint32
	shortTail bool
}

func (p *receivePagePolicy) size(hot bool) uint32 {
	if p.limit == 0 {
		p.limit = 4
	}
	if hot && !p.shortTail {
		return min(p.limit, 4)
	}
	return p.limit
}

// Only a completed page proves that widening a hot turn won't strand a short
// response behind large bodies. Unknown and large mail retain the four-UID cap.
func (p *receivePagePolicy) observeBodyShape(rows, largestMIME int) {
	if rows > 0 {
		p.shortTail = largestMIME > 0 && largestMIME <= 32<<10
	}
}
func (p *receivePagePolicy) observe(limit uint32, rows int, elapsed time.Duration) {
	if elapsed >= 300*time.Millisecond {
		p.limit = 4
		return
	}
	if rows >= int(limit) {
		p.limit = min(uint32(16), max(uint32(4), limit*2))
	} else if rows == 0 {
		p.limit = max(uint32(4), limit/2)
	}
}

// Propagation alone is not receiver queueing. Preserve the bounded backlog
// drain on high-RTT links while reacting to extra service/serialization delay.
func (p *receivePagePolicy) observeWithRTT(limit uint32, rows int, elapsed, baseline time.Duration) {
	p.observe(limit, rows, max(time.Duration(0), elapsed-min(baseline, 2*time.Second)))
}
