package datapath

import "sort"

// Only scalar metadata is exposed. All engine inspection remains on its actor;
// callers read an immutable snapshot without touching live flow ownership.
type StreamSchedulerSnapshot struct{ Stream, SendNext, SendReceipt, ReceiveReceipt, ReceiveDelivered uint64 }
type SchedulerSnapshot struct {
	Streams                                                    []StreamSchedulerSnapshot
	LogicalBytes                                               uint64
	Items, Attempts, Queued, Writing, RepairReady, WakeBlocked int
	BulkQueued, FastQueued                                     int
	OldestItem, OldestStream, OldestOffset                     uint64
	OldestPhase                                                string
}

func (p *Pump) captureScheduler() {
	s := &SchedulerSnapshot{LogicalBytes: p.engine.tx.LogicalBytes(), Items: p.engine.tx.ItemCount(), Attempts: p.engine.tx.AttemptCount(), BulkQueued: len(p.dataBulk), FastQueued: len(p.dataFast) + len(p.dataTiny)}
	for id, entry := range p.engine.pending {
		phase := "WAITING_ACK"
		if p.engine.tx.IsQueued(id) {
			s.Queued++
			phase = "QUEUED"
		}
		if entry.requested {
			s.RepairReady++
			phase = "REPAIR_READY"
		}
		if entry.writingAttempt != 0 {
			s.Writing++
			phase = "WRITING"
		}
		if wake := p.retryWake[id]; wake != nil {
			select {
			case <-wake:
			default:
				s.WakeBlocked++
				phase = "WAKE_BLOCKED"
			}
		}
		if s.OldestItem == 0 || id < s.OldestItem {
			f, _, err := p.engine.FrameInfo(p.cfg.Engine.Generation, id)
			if err == nil {
				s.OldestItem, s.OldestStream, s.OldestOffset, s.OldestPhase = id, f.Stream, f.Offset, phase
			}
		}
	}
	ids := make([]uint64, 0, len(p.engine.streams))
	for id := range p.engine.streams {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		next, receipt := p.engine.tx.StreamFrontier(id)
		rx, delivered, _, _ := p.engine.rx.Frontier(id)
		s.Streams = append(s.Streams, StreamSchedulerSnapshot{id, next, receipt, rx, delivered})
	}
	p.scheduler.Store(s)
}
func (p *Pump) SchedulerSnapshot() SchedulerSnapshot {
	if p != nil {
		if s := p.scheduler.Load(); s != nil {
			result := *s
			result.Streams = append([]StreamSchedulerSnapshot(nil), s.Streams...)
			return result
		}
	}
	return SchedulerSnapshot{}
}
