package mailstream

import "time"

// Application credit limits memory, not network queues. Limit publication
// separately using authenticated delivery receipts. A fast mailbox server must
// not dump the entire 32 MiB memory allowance onto a slow mobile reader.
// All fields are owned by Session.mu.
type deliveryWindow struct {
	flight int
	limit  int
	base   time.Duration
	start  time.Time
	acked  int
	rate   float64
}

const initialDeliveryWindow = 512 << 10

func (w *deliveryWindow) allowance() int {
	if w.limit == 0 {
		return initialDeliveryWindow
	}
	return w.limit
}

func (w *deliveryWindow) delivered(n int, sample time.Duration, now time.Time) {
	w.flight -= n
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
	// Startup explores by delivered bytes, so faster paths grow faster.
	if sample > 0 && sample <= 2*w.base {
		w.limit = min(maxPendingBytes, w.limit+n)
	}
	if elapsed := now.Sub(w.start); elapsed >= 250*time.Millisecond {
		rate := float64(w.acked) / elapsed.Seconds()
		if w.rate == 0 {
			w.rate = rate
		} else {
			w.rate = (w.rate + rate) / 2
		}
		// Allow throughput-oriented headroom under load, without admitting
		// tens of seconds of work merely because receive memory is free.
		target := int(w.rate * (2*w.base + 250*time.Millisecond).Seconds())
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
