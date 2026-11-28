package mailstream

import (
	"context"
	"encoding/binary"
	"errors"
	"sync"
	"time"
)

// One receive owner reserves feedback capacity before applying a record.
// The worker owns transmission and keeps a frozen batch charged until its
// immutable ciphertext is accepted. Duplicate input cannot fill the queue.
// A separate reserve lets interactive/control records survive DATA pressure.
type receiptRequest struct {
	sequence uint64
	bulk     bool
}
type receiptQueue struct {
	mu             sync.Mutex
	pending        map[uint64]struct{}
	urgent, bulk   []uint64
	limit, reserve int
	wake           chan struct{}
}

func newReceiptQueue(limit, reserve int) *receiptQueue {
	return &receiptQueue{pending: make(map[uint64]struct{}, limit), limit: limit, reserve: reserve, wake: make(chan struct{}, 1)}
}
func (q *receiptQueue) canAccept(seq uint64, bulk bool) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.pending[seq]; ok {
		return true
	}
	limit := q.limit
	if bulk {
		limit -= q.reserve
	}
	return len(q.pending) < limit
}
func (q *receiptQueue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}
func (q *receiptQueue) offer(req receiptRequest) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.pending[req.sequence]; ok {
		return true
	}
	limit := q.limit
	if req.bulk {
		limit -= q.reserve
	}
	if len(q.pending) >= limit {
		return false
	}
	q.pending[req.sequence] = struct{}{}
	if req.bulk {
		q.bulk = append(q.bulk, req.sequence)
	} else {
		q.urgent = append(q.urgent, req.sequence)
	}
	q.signal()
	return true
}
func (q *receiptQueue) take(batch []receiptRequest, limit int) []receiptRequest {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(batch) < limit && len(q.urgent) > 0 {
		batch = append(batch, receiptRequest{q.urgent[0], false})
		q.urgent = q.urgent[1:]
	}
	for len(batch) < limit && len(q.bulk) > 0 {
		batch = append(batch, receiptRequest{q.bulk[0], true})
		q.bulk = q.bulk[1:]
	}
	if len(q.urgent)+len(q.bulk) > 0 {
		q.signal()
	}
	return batch
}
func (q *receiptQueue) complete(batch []receiptRequest) {
	q.mu.Lock()
	for _, r := range batch {
		delete(q.pending, r.sequence)
	}
	q.mu.Unlock()
}
func (q *receiptQueue) size() int { q.mu.Lock(); defer q.mu.Unlock(); return len(q.pending) }
func (s *Session) receiptLoop() {
	q := s.receipts
	batch := make([]receiptRequest, 0, 32)
	for {
		batch = q.take(batch[:0], 32)
		if len(batch) == 0 {
			select {
			case <-s.ctx.Done():
				return
			case <-q.wake:
				continue
			}
		}
		started := time.Now()
		deadline := started.Add(8 * time.Millisecond)
		for _, r := range batch {
			if !r.bulk {
				deadline = started.Add(2 * time.Millisecond)
				break
			}
		}
		for len(batch) < 32 && time.Now().Before(deadline) {
			timer := time.NewTimer(time.Until(deadline))
			select {
			case <-s.ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			case <-q.wake:
				timer.Stop()
				previous := len(batch)
				batch = q.take(batch, 32)
				for _, r := range batch[previous:] {
					if !r.bulk {
						deadline = minTime(deadline, time.Now().Add(2*time.Millisecond))
						break
					}
				}
			}
		}
		body := make([]byte, 0, 8*len(batch))
		for _, r := range batch {
			body = binary.BigEndian.AppendUint64(body, r.sequence)
		}
		plain, e := (frame{kind: frameReceipt, data: body}).encode()
		if e != nil {
			s.fail(e)
			return
		}
		wire, _, e := s.cipher.seal(plain)
		if e != nil {
			s.fail(e)
			return
		}
		for {
			var err error
			if feedback, ok := s.link.(interface {
				SendFeedback(context.Context, []byte) error
			}); ok {
				err = feedback.SendFeedback(s.ctx, wire)
			} else {
				err = s.link.Send(s.ctx, wire, true)
			}
			if err == nil {
				q.complete(batch)
				break
			}
			if !errors.Is(err, ErrCapacity) {
				s.fail(err)
				return
			}
			s.receiptRetries.Add(1)
			timer := time.NewTimer(2 * time.Millisecond)
			select {
			case <-s.ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}
}
