package mailstream

import "time"

// Application credit limits memory, not network queues. Limit publication
// separately using authenticated delivery receipts. A fast mailbox server must
// not dump the entire 32 MiB memory allowance onto a slow mobile reader.
// All fields are owned by Session.mu.
type deliveryMark struct {
	delivered                    uint64
	deliveredAt, firstSent, sent time.Time
}

type deliveryWindow struct {
	bulkFlight             int
	totalDelivered         uint64
	deliveredAt, firstSent time.Time
	rateSampleMax          float64
	flight                 int
	limit                  int
	base                   time.Duration
	start                  time.Time
	last                   time.Time
	acked                  int
	rate                   float64
	ratePeak               float64
	ratePeakAt             time.Time
	limited                bool // flight allowance blocked a queued bulk packet since the last rate sample
	bulkRTT                time.Duration
	horizon                time.Duration
	adaptive               bool
}

const initialDeliveryWindow = 512 << 10

// Leave 250 ms of the two-second loaded-RTT budget for protocol/control.
// This is a ceiling on measured-rate headroom, never an initial flight grant.
const maxBulkDeliveryHorizon = 1750 * time.Millisecond

func (w *deliveryWindow) allowance() int {
	if w.limit == 0 {
		return initialDeliveryWindow
	}
	return w.limit
}

func (w *deliveryWindow) delivered(n int, sample time.Duration, now time.Time, bulk bool, marks ...deliveryMark) {
	w.flight -= n
	// Interactive DATA bypasses the bulk allowance; its smaller, faster receipts
	// cannot train the capacity estimate or minimum RTT of the bulk carrier.
	if !bulk {
		return
	}
	// Light probes and an idle opposite direction are not capacity samples.
	// Restart from a safe flight bound, without dividing bytes by an idle gap.
	if w.last.IsZero() || now.Sub(w.last) > max(time.Second, 4*w.base) {
		w.start, w.acked, w.rate = now, 0, 0
		w.limit = initialDeliveryWindow
		w.bulkRTT, w.horizon = 0, 0
		w.rateSampleMax = 0
		w.ratePeak, w.ratePeakAt = 0, time.Time{}
	}
	if len(marks) > 0 && !marks[0].sent.IsZero() {
		m := marks[0]
		w.bulkFlight = max(0, w.bulkFlight-n)
		w.totalDelivered += uint64(n)
		// Use both the sending interval and the delivery interval recorded
		// with this packet. A compressed ACK train cannot turn a slow link
		// into an apparently fast one merely by arriving within 250 ms.
		interval := max(m.sent.Sub(m.firstSent), now.Sub(m.deliveredAt))
		if interval > 0 && w.totalDelivered >= m.delivered {
			w.rateSampleMax = max(w.rateSampleMax, float64(w.totalDelivered-m.delivered)/interval.Seconds())
		}
		w.deliveredAt = now
		if m.sent.After(w.firstSent) {
			w.firstSent = m.sent
		}
	}
	w.last = now
	if sample > 0 {
		if w.bulkRTT == 0 {
			w.bulkRTT = sample
		} else {
			w.bulkRTT = (7*w.bulkRTT + sample) / 8
		}
	}
	if sample > 0 && (w.base == 0 || sample < w.base) {
		w.base = sample
	}
	if w.start.IsZero() {
		w.start = now
	}
	w.acked += n
	if w.limit == 0 {
		w.limit = initialDeliveryWindow
	}
	// Grow on delivered bytes while real queued work was flight-limited.
	// Waiting for a 250 ms estimator interval wastes several fast receipt
	// rounds at the start of a short transfer. Quiet/light traffic cannot grow it.
	lowQueue := w.bulkRTT <= w.base+250*time.Millisecond
	if w.limited {
		if lowQueue {
			w.limit = min(maxPendingBytes, w.limit+n)
		} else if ceiling := int(w.rate * maxBulkDeliveryHorizon.Seconds()); ceiling > w.allowance() {
			// Spend already measured headroom on the ACK clock rather than
			// pausing another estimator interval on every slow receipt round.
			w.limit = min(maxPendingBytes, min(ceiling, w.limit+n))
		}
	}
	// Grow from measured delivered rate, not a delay threshold or an entire
	// extra flight per clustered ACK batch. The initial flight is enough to
	// measure a fast path, while doubling it before a sample floods a 7 Mbps link.
	if elapsed := now.Sub(w.start); elapsed >= 250*time.Millisecond {
		rate := float64(w.acked) / elapsed.Seconds()
		if w.rateSampleMax > 0 {
			rate = min(rate, w.rateSampleMax)
		}
		w.rateSampleMax = 0
		// A single sparse receipt batch is not a capacity drop. Keep a short
		// peak of compression-safe delivery samples; expire it promptly when
		// a genuinely slower path supplies lower samples for a full second.
		if rate >= w.ratePeak || now.Sub(w.ratePeakAt) >= time.Second {
			w.ratePeak, w.ratePeakAt = rate, now
		}
		w.rate = w.ratePeak
		// Use the minimum bulk path RTT plus a finite queue allowance. A
		// small setup response can have a much faster FETCH/receipt path than
		// full DATA. When we blocked queued bytes, probe beyond that baseline
		// using measured delivery rate. Otherwise rate * baseline reinforces
		// a 512 KiB / bulk-RTT ceiling forever, even on a capable mobile link.
		horizon := min(maxBulkDeliveryHorizon, max(500*time.Millisecond, w.base+250*time.Millisecond))
		if w.limited {
			horizon = max(horizon, min(maxBulkDeliveryHorizon, 2*w.bulkRTT))
		}
		w.horizon = horizon
		target := int(w.rate * horizon.Seconds())
		if !lowQueue {
			// Slow/compressed callbacks may permit a rate-based probe, never
			// an unmeasured extra flight. Grow by at most bytes just delivered;
			// the independent send/delivery clock still bounds the rate sample.
			target = min(target, w.allowance()+w.acked)
		}
		if lowQueue {
			// A partially filled publication or sparse receipt sample is not
			// congestion. Preserve the byte-clocked allowance until queue delay
			// provides evidence that it should contract.
			target = max(target, w.allowance())
		}
		w.limited = false
		w.limit = min(maxPendingBytes, max(initialDeliveryWindow, target))
		w.start, w.acked = now, 0
	}
}

func retryAfter(p *pendingPacket, rto time.Duration) time.Duration {
	// IMAP/TCP queueing is not datagram loss. Repeating an entire large
	// MIME body every RTO amplified the backlog that delayed its receipt.
	if !p.priority {
		rto = max(rto, 3*time.Second)
	}
	return min(time.Minute, rto*time.Duration(1<<min(max(p.attempts-1, 0), 5)))
}

// A speedtest may close a transfer while its sender still has read-ahead
// queued. RESET makes those bytes obsolete; keeping them in the reliable retry
// set starved unrelated browsing long after the speedtest had ended.
func (s *Session) abandonStream(stream uint32) {
	var sequences []uint64
	s.mu.Lock()
	for seq, p := range s.pending {
		if p.stream != stream || (p.kind != frameData && p.kind != frameFIN && p.kind != frameOpen && p.kind != frameOpenOK) {
			continue
		}
		s.pendingBytes -= len(p.wire)
		if p.kind == frameData && p.attempts > 0 {
			s.delivery.flight -= len(p.wire)
			if !p.priority {
				s.delivery.bulkFlight = max(0, s.delivery.bulkFlight-len(p.wire))
			}
		}
		delete(s.pending, seq)
		sequences = append(sequences, seq)
	}
	s.notifyLocked()
	s.mu.Unlock()
	if storage, ok := s.link.(interface{ Abandon(SessionID, uint64) }); ok {
		for _, seq := range sequences {
			storage.Abandon(s.cipher.id, seq)
		}
	}
}

// Capture delivery state at first publication. The initial flight of a fresh
// burst starts a new sending interval, excluding application-idle time.
func (w *deliveryWindow) sent(n int, now time.Time, bulk bool) deliveryMark {
	w.flight += n
	if !bulk || !w.adaptive {
		return deliveryMark{}
	}
	if w.bulkFlight == 0 {
		w.firstSent = now
	}
	w.bulkFlight += n
	at := w.deliveredAt
	if at.Before(w.firstSent) {
		at = w.firstSent
	}
	return deliveryMark{w.totalDelivered, at, w.firstSent, now}
}
