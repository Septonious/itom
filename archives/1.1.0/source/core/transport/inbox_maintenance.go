package transport

import (
	"context"
	"errors"
	"time"
)

// Maintenance shares the SAME selected executor, but never holds Inbox.mu while
// waiting on STORE/EXPUNGE/proof tags. Reception and application replies keep
// progressing. Disjoint singleton BODY/FLAGS can share the one bounded parser;
// inventory/absence remain exclusive. No second reader, connection, inventory
// assumption or allowance is introduced.
// Run joins this helper before handing worker ownership back after cancellation.
func (w *InboxWorker) maintain(ctx context.Context) error {
	i, x := w.inbox, w.inbox.executor
	idleDelay := w.limits.MinProbe
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !w.maintenanceAllowed() {
			timer := time.NewTimer(w.limits.MaxProbe)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-x.done:
				timer.Stop()
				return ErrClosed
			case <-timer.C:
			}
			continue
		}
		space := x.SpaceChanged()
		begin := time.Now()
		marked, err := i.markReceiptOne(ctx)
		reclaimed := false
		if err == nil {
			reclaimed, err = i.reclaimOne(ctx)
		}
		if errors.Is(err, errCommandCapacity) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-x.done:
				return ErrClosed
			case <-space:
				continue
			}
		}
		if err != nil {
			return err
		}
		if marked || reclaimed {
			idleDelay = w.limits.MinProbe
			continue
		}
		i.mu.Lock()
		pending := i.receiptEnabled && len(i.receipts) > 0 || i.reclamation && len(i.reclaimQueue) > 0
		wake := i.maintenanceChanged
		i.mu.Unlock()
		var timer *time.Timer
		var tick <-chan time.Time
		if pending {
			// Repeating an absent receipt/absence proof cannot create progress.
			// Back off only these unsuccessful probes, within the ORIGINAL
			// bounds. A new queued candidate wakes immediately; real marking or
			// reclamation resets the delay and ready work drains without a timer.
			serviceFloor := min(w.limits.MaxProbe, time.Since(begin)/8)
			idleDelay = max(w.limits.MinProbe, serviceFloor, min(w.limits.MaxProbe, idleDelay*2))
			timer = time.NewTimer(idleDelay)
			tick = timer.C
		}
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return ctx.Err()
		case <-x.done:
			if timer != nil {
				timer.Stop()
			}
			return ErrClosed
		case <-wake:
		case <-tick:
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

// Yield optional physical cleanup while short foreground work owns the path.
// Nothing is refunded here. Already issued tags keep their normal draining
// lifetime. Quiet service, bulk demand, publication pressure or bounded queued
// maintenance re-enable the original receipt/EXPUNGE/absence-proof machinery.
func (w *InboxWorker) maintenanceAllowed() bool {
	s := w.inbox.session
	s.mu.Lock()
	runtime := s.runtime
	s.mu.Unlock()
	if runtime == nil {
		return true
	}
	if s.historicalLaunchAllowed(time.Now()) {
		return true
	}
	i := w.inbox
	i.mu.Lock()
	pressure := len(i.receipts)+len(i.reclaimQueue) >= maxAuthorityQueued/2
	i.mu.Unlock()
	if pressure {
		return true
	}
	if runtime != nil {
		for _, c := range runtime.carriers {
			stats := c.Writer.Stats()
			if stats.DataBlocked || stats.ControlBlocked {
				return true
			}
		}
	}
	return false
}
