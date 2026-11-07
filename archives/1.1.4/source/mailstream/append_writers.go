package mailstream

import (
	"context"
	"sync"
)

// Four real, independently authenticated IMAP connections per active account:
// control read, control APPEND, DATA read, and DATA APPEND. A single APPEND actor
// owns both writers and fences every tagged completion before any mutation.
const ConnectionsPerAccount = 4

// An uncertain command never refunds retention. Close both APPEND transports
// before joining their shared completion group: a blocked control tag must not
// strand collection after the DATA socket has already timed out.
func (a *mailAccountState) reopenAppendWriters(attempt context.Context, writer **mailPipe, completed chan error, inFlight *int, wait *sync.WaitGroup) {
	a.mu.Lock()
	control := a.controlWriter
	a.controlWriter, a.writer = nil, nil
	a.mu.Unlock()
	if *writer != nil {
		(*writer).close()
	}
	if control != nil {
		control.close()
	}
	wait.Wait()
	for {
		select {
		case <-completed:
		default:
			goto drained
		}
	}
drained:
	*inFlight = 0
	*writer = nil
	if a.parking.Load() || a.ctx.Err() != nil {
		return
	}
	fresh, err := openMailPipeAttempt(attempt, a.ctx, a.config)
	if err != nil {
		return
	}
	*writer = fresh
	var companion *mailPipe
	if control != nil {
		companion, _ = openMailPipeAttempt(attempt, a.ctx, a.config)
	}
	a.mu.Lock()
	a.writer, a.controlWriter = fresh, companion
	a.mu.Unlock()
}
