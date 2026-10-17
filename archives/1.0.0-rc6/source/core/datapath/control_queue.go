package datapath

import (
	"itom.local/core/flow"
	"itom.local/core/wire"
	"sync"
)

type controlKey struct {
	stream uint64
	kind   wire.Type
}
type pendingControls struct {
	mu     sync.Mutex
	latest map[controlKey]wire.Frame
	order  []controlKey
	wake   chan struct{}
}

// These controls are cumulative snapshots or immutable terminal replies. A
// newer unsent record supersedes the earlier one. Attempted/sealed records,
// NACKs and durable quota descriptors never enter this queue.
func reusableControl(f wire.Frame) bool {
	switch f.Type {
	case wire.Status, wire.Window, wire.Receipt, wire.Fin, wire.FinAck, wire.Reset, wire.ResetAck:
		return f.Stream != 0
	}
	return false
}

func (p *Pump) queueSnapshot(f wire.Frame) error {
	q := &p.pendingControls
	q.mu.Lock()
	defer q.mu.Unlock()
	key := controlKey{f.Stream, f.Type}
	if q.latest == nil {
		q.latest = make(map[controlKey]wire.Frame)
	}
	if previous, exists := q.latest[key]; exists {
		// FIN/RESET finals are immutable; cumulative WINDOW/RECEIPT cannot regress.
		if f.Type == wire.Status {
			merged, ok := wire.MergeProgressFrames(previous, f)
			if !ok {
				return ErrState
			}
			f = merged
		} else if f.Offset < previous.Offset {
			return nil
		}
		q.latest[key] = f
		return nil
	}
	// Seven kinds per active stream plus the existing burst reserve. This remains
	// bounded even if a peer or application opens thousands of short sockets.
	if len(q.latest) >= 7*p.cfg.Engine.Limits.Streams+max(p.cfg.ControlQueue, cap(p.controls)) {
		return flow.ErrCredit
	}
	q.latest[key] = f
	q.order = append(q.order, key)
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return nil
}

func (p *Pump) takeSnapshot() (wire.Frame, bool) {
	q := &p.pendingControls
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.order) == 0 {
		return wire.Frame{}, false
	}
	key := q.order[0]
	copy(q.order, q.order[1:])
	q.order = q.order[:len(q.order)-1]
	f := q.latest[key]
	delete(q.latest, key)
	return f, true
}
