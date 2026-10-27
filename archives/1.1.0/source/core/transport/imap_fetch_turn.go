package transport

import "context"

// Only a BODY and FLAGS query for disjoint UIDs can coexist. Their response
// forms identify them independently, including when tags finish out of order.
// Inventory, absence proof and UID EXPUNGE remain exclusive; hints cannot supply
// proof. EXPUNGE uses this same FIFO so sequence mutation cannot cross read tags.
type fetchRequest struct {
	op                         *operation
	ready                      chan struct{}
	granted, started, finished bool
}

func (e *Executor) compatibleFetches(a, b *operation) bool {
	if !((a.class == commandBody && b.class == commandFlags) || (a.class == commandFlags && b.class == commandBody)) {
		return false
	}
	flags, body := a, b
	if flags.class != commandFlags {
		flags, body = b, a
	}
	// Bulk groups and narrow custom response budgets keep exclusive service.
	// A singleton literal plus its framing and one FLAGS line per requested
	// UID must fit without borrowing a second response-buffer allowance.
	if body.first != body.last || e.limits.LiteralBytes+(len(flags.requested)+2)*e.limits.LineBytes > e.limits.ResponseBytes {
		return false
	}
	for uid := range flags.requested {
		if body.requestsUID(uid) {
			return false
		}
	}
	return len(flags.requested) > 0
}

// FIFO admission happens before the wire writer. A blocked exclusive request
// cannot be bypassed by later probes, so cleanup and inventory cannot starve.
// At most two owners hold turns, within the original pending-tag bound.
func (e *Executor) grantFetchTurnsLocked() {
	if e.closed != nil {
		return
	}
	for len(e.fetchQueue) > 0 {
		r := e.fetchQueue[0]
		slot := -1
		allowed := true
		for n, active := range e.fetchTurns {
			if active == nil {
				slot = n
				continue
			}
			if !e.compatibleFetches(r.op, active.op) {
				allowed = false
			}
		}
		// A public fail-fast FETCH never participates in overlap.
		if e.fetch != nil && e.fetch.fetchTurn == nil || e.flagFetch != nil && e.flagFetch.fetchTurn == nil {
			allowed = false
		}
		if !allowed || slot < 0 {
			return
		}
		e.fetchQueue[0] = nil
		e.fetchQueue = e.fetchQueue[1:]
		e.fetchTurns[slot] = r
		r.granted = true
		close(r.ready)
	}
}

func (e *Executor) releaseFetchRequestLocked(r *fetchRequest) {
	if r.finished {
		return
	}
	r.finished = true
	for n, active := range e.fetchTurns {
		if active == r {
			e.fetchTurns[n] = nil
		}
	}
	for n, queued := range e.fetchQueue {
		if queued == r {
			copy(e.fetchQueue[n:], e.fetchQueue[n+1:])
			e.fetchQueue[len(e.fetchQueue)-1] = nil
			e.fetchQueue = e.fetchQueue[:len(e.fetchQueue)-1]
			break
		}
	}
	e.grantFetchTurnsLocked()
}

func (e *Executor) acquireFetchTurn(ctx context.Context, op *operation) (*fetchRequest, error) {
	e.mu.Lock()
	if err := ctx.Err(); err != nil {
		e.mu.Unlock()
		return nil, err
	}
	if e.closed != nil {
		e.mu.Unlock()
		return nil, ErrClosed
	}
	if len(e.fetchQueue) >= e.limits.Pending {
		e.mu.Unlock()
		return nil, errCommandCapacity
	}
	r := &fetchRequest{op: op, ready: make(chan struct{})}
	op.fetchTurn = r
	e.fetchQueue = append(e.fetchQueue, r)
	e.fetchWaiters++
	e.grantFetchTurnsLocked()
	e.mu.Unlock()
	defer func() { e.mu.Lock(); e.fetchWaiters--; e.mu.Unlock() }()
	var err error
	select {
	case <-r.ready:
		if err = ctx.Err(); err == nil {
			return r, nil
		}
	case <-ctx.Done():
		err = ctx.Err()
	case <-e.done:
		err = ErrClosed
	}
	e.mu.Lock()
	e.releaseFetchRequestLocked(r)
	e.mu.Unlock()
	return nil, err
}

// Once issued, the tag owns the turn, not its detachable application waiter.
func (e *Executor) releaseUnstartedFetch(r *fetchRequest) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !r.started {
		e.releaseFetchRequestLocked(r)
	}
}
func (e *Executor) releaseFetchTurnLocked(op *operation) {
	if op.fetchTurn != nil {
		e.releaseFetchRequestLocked(op.fetchTurn)
		op.fetchTurn = nil
	} else {
		e.grantFetchTurnsLocked()
	}
}

// All simultaneous FETCH responses share the ORIGINAL response-byte budget.
func (e *Executor) chargeFetchLocked(op *operation, n int) error {
	if n < 0 || n > e.limits.ResponseBytes-op.bytes || n > e.limits.ResponseBytes-e.fetchBytes {
		return ErrCapacity
	}
	op.bytes += n
	e.fetchBytes += n
	return nil
}
