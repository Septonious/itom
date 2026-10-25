package transport

import "context"

// Receive discovery and maintenance have one independent owner each, but share
// one untagged FETCH parser. Queue their turns BEFORE taking the wire writer.
// The channel hands a released slot to an already waiting owner before a new
// polling turn can acquire it. Ordinary public commands retain fail-fast bounds.
// At most one command is issued; no response, pending-tag or socket limit grows.
func (e *Executor) acquireFetchTurn(ctx context.Context) (func(*operation), error) {
	e.mu.Lock()
	e.fetchWaiters++
	e.mu.Unlock()
	defer func() { e.mu.Lock(); e.fetchWaiters--; e.mu.Unlock() }()
	select {
	case e.fetchTurn <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-e.done:
		return nil, ErrClosed
	}
	// Ownership transfers to the actual operation, NOT its detachable waiter.
	// A canceled Wait must not admit another untagged response ahead of this tag.
	return func(op *operation) {
		e.mu.Lock()
		defer e.mu.Unlock()
		if op != nil {
			select {
			case <-op.done:
			default:
				op.fetchTurn = e.fetchTurn
				return
			}
		}
		<-e.fetchTurn
	}, nil
}

func (e *Executor) releaseFetchTurnLocked(op *operation) {
	if op.fetchTurn != nil {
		<-op.fetchTurn
		op.fetchTurn = nil
	}
}
