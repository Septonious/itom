package tunnel

import (
	"context"
	"errors"
	"time"

	"itom.local/core/wire"
)

type flowControlKey struct {
	kind   wire.Type
	stream uint64
}

// WINDOW and logical RECEIPT are cumulative. Keep only the newest unsent
// update per stream/type, before writer backpressure can fill the bounded
// channel with obsolete credit updates. No sealed/attempted record is replaced.
func (l *lane) enqueueFlowControl(ctx context.Context, item queuedControl) error {
	key := flowControlKey{item.Type, item.Stream}
	deadline := time.NewTimer(250 * time.Millisecond)
	defer deadline.Stop()
	for {
		l.flowControlMu.Lock()
		if l.flowControlLatest == nil {
			l.flowControlLatest = map[flowControlKey]queuedControl{}
		}
		if previous, found := l.flowControlLatest[key]; found {
			if item.Type == wire.Status {
				merged, ok := wire.MergeProgressFrames(previous.Frame, item.Frame)
				if !ok {
					l.flowControlMu.Unlock()
					return errors.New("PROGRESS_STATE")
				}
				item.Frame = merged
			} else if item.Offset < previous.Offset {
				l.flowControlMu.Unlock()
				return nil
			}
			item.added = previous.added
			// Only the queue's original birth time is preserved. The
			// newest merged ACK payload was just observed.
			item.latestAt = time.Now()
			l.flowControlLatest[key] = item
			l.flowControlMu.Unlock()
			return nil
		}
		item.coalesced = true
		if len(l.flowControlLatest) < cap(l.controlQueue) {
			queue := l.controlQueue
			if latencyControl(item.Frame) {
				queue = l.urgentQueue
			}
			select {
			case queue <- item:
				l.flowControlLatest[key] = item
				l.flowControlMu.Unlock()
				return nil
			default:
			}
		}
		l.flowControlMu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-l.ctx.Done():
			return l.ctx.Err()
		case <-deadline.C:
			return errors.New("CONTROL_QUEUE_LIMIT")
		case <-time.After(time.Millisecond):
		}
	}
}

func (l *lane) resolveFlowControl(item queuedControl) queuedControl {
	if !item.coalesced {
		return item
	}
	key := flowControlKey{item.Type, item.Stream}
	l.flowControlMu.Lock()
	if latest, found := l.flowControlLatest[key]; found {
		item = latest
		delete(l.flowControlLatest, key)
	}
	l.flowControlMu.Unlock()
	item.coalesced = false
	return item
}

func coalesceCleanupStatus(batch []queuedControl, item queuedControl) []queuedControl {
	if item.Type != wire.GCStatus {
		return append(batch, item)
	}
	incoming, err := wire.DecodeControl(item.Frame)
	if err != nil {
		return append(batch, item)
	}
	next, ok := incoming.(wire.CleanupStatus)
	if !ok {
		return append(batch, item)
	}
	for i, old := range batch {
		if old.Type != wire.GCStatus {
			continue
		}
		decoded, err := wire.DecodeControl(old.Frame)
		if err != nil {
			continue
		}
		previous, ok := decoded.(wire.CleanupStatus)
		if !ok || previous.Key != next.Key {
			continue
		}
		dominates := func(a, b wire.CleanupStatus) bool {
			return a.Used.Bytes >= b.Used.Bytes && a.Used.Messages >= b.Used.Messages && a.Registered.Bytes >= b.Registered.Bytes && a.Registered.Messages >= b.Registered.Messages && a.Released.Bytes >= b.Released.Bytes && a.Released.Messages >= b.Released.Messages && (!b.Closed || a.Closed)
		}
		if dominates(next, previous) {
			item.added = old.added
			batch[i] = item
			return batch
		}
		if dominates(previous, next) {
			return batch
		}
	}
	return append(batch, item)
}

// Only established bulk receiver progress uses feedback affinity. Initial credit,
// terminal controls, repairs and small responses retain their interactive route.
func bulkReceiveFeedback(f wire.Frame) bool {
	switch f.Type {
	case wire.Receipt:
		return f.Offset > 64<<10
	case wire.Window:
		return f.Offset > runtimeStreamWindow+64<<10
	case wire.Status:
		c, err := wire.DecodeControl(f)
		if err != nil {
			return false
		}
		p := c.(wire.Progress)
		return !p.HasFinal && p.Receipt > 64<<10
	}
	return false
}

// Updating an existing unsent snapshot adds no queue/debt. Penalizing its own
// queued token during carrier selection scatters each subsequent ACK instead
// of replacing that token; consult under the normal group -> queue lock order.
func (l *lane) hasPendingFlowControl(kind wire.Type, stream uint64) bool {
	l.flowControlMu.Lock()
	defer l.flowControlMu.Unlock()
	_, ok := l.flowControlLatest[flowControlKey{kind, stream}]
	return ok
}

// Unsent cumulative registration/deletion status is replaced only by a
// dominating snapshot of the same original grant. Unknown physical debt and
// terminal fencing never disappear through coalescing.
func (l *lane) enqueueQuotaControl(ctx context.Context, item queuedControl) error {
	decoded, err := wire.DecodeControl(item.Frame)
	if err != nil {
		return err
	}
	next, ok := decoded.(wire.CleanupStatus)
	if !ok {
		return errors.New("QUOTA_STATUS_TYPE")
	}
	l.quotaControlMu.Lock()
	defer l.quotaControlMu.Unlock()
	if l.quotaControlLatest == nil {
		l.quotaControlLatest = map[wire.QuotaKey]queuedControl{}
	}
	if previous, exists := l.quotaControlLatest[next.Key]; exists {
		merged := coalesceCleanupStatus([]queuedControl{previous}, item)
		if len(merged) != 1 {
			return errors.New("QUOTA_STATUS_ORDER")
		}
		latest := merged[0]
		latest.quotaCoalesced = true
		latest.quotaKey = next.Key
		l.quotaControlLatest[next.Key] = latest
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(l.quotaControlLatest) >= 512 {
		return errors.New("CONTROL_QUEUE_LIMIT")
	}
	item.quotaCoalesced = true
	item.quotaKey = next.Key
	queue := l.maintenanceQueue
	if queue == nil {
		queue = l.controlQueue
	}
	select {
	case queue <- item:
		l.quotaControlLatest[next.Key] = item
		return nil
	default:
		return errors.New("CONTROL_QUEUE_LIMIT")
	}
}
func (l *lane) resolveQuotaControl(item queuedControl) queuedControl {
	if !item.quotaCoalesced {
		return item
	}
	l.quotaControlMu.Lock()
	defer l.quotaControlMu.Unlock()
	if latest, exists := l.quotaControlLatest[item.quotaKey]; exists {
		item = latest
		delete(l.quotaControlLatest, item.quotaKey)
	}
	item.quotaCoalesced = false
	return item
}
