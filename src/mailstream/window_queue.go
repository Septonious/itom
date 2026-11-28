package mailstream

import (
	"encoding/binary"
	"errors"
	"sync"
	"time"
)

// Absolute consumption credit is replaceable. Keep only the latest unqueued
// update for each stream, so an application read never waits on publication.
// Once a WINDOW is encrypted, it remains an immutable reliable record.
type windowUpdate struct {
	consumed uint64
	limit    int
}
type windowQueue struct {
	mu      sync.Mutex
	pending map[uint32]windowUpdate
	wake    chan struct{}
}

func newWindowQueue() *windowQueue {
	return &windowQueue{pending: make(map[uint32]windowUpdate), wake: make(chan struct{}, 1)}
}
func (q *windowQueue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}
func (q *windowQueue) offer(id uint32, consumed uint64, limit int) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	prev, ok := q.pending[id]
	if !ok && len(q.pending) >= 2*maxStreams {
		return false
	}
	q.pending[id] = windowUpdate{max(prev.consumed, consumed), max(prev.limit, limit)}
	q.signal()
	return true
}
func (q *windowQueue) take() (uint32, windowUpdate, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for id, w := range q.pending {
		return id, w, true
	}
	return 0, windowUpdate{}, false
}
func (q *windowQueue) complete(id uint32, w windowUpdate) {
	q.mu.Lock()
	if q.pending[id] == w {
		delete(q.pending, id)
	}
	q.mu.Unlock()
}
func (q *windowQueue) discard(id uint32) { q.mu.Lock(); delete(q.pending, id); q.mu.Unlock() }
func (s *Session) publishWindow(id uint32, consumed uint64, limit int) error {
	if !s.windows.offer(id, consumed, limit) {
		return ErrCapacity
	}
	return nil
}
func (s *Session) windowLoop() {
	for {
		id, w, ok := s.windows.take()
		if !ok {
			select {
			case <-s.ctx.Done():
				return
			case <-s.windows.wake:
				continue
			}
		}
		f := frame{kind: frameWindow, stream: id, offset: w.consumed, data: binary.BigEndian.AppendUint32(nil, uint32(w.limit))}
		err := s.trySend(f, true)
		if err == nil {
			s.windows.complete(id, w)
			continue
		}
		if !errors.Is(err, ErrCapacity) {
			if s.ctx.Err() == nil {
				s.fail(err)
			}
			return
		}
		timer := time.NewTimer(2 * time.Millisecond)
		select {
		case <-s.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
