package mailstream

import (
	"context"
	"sync"
	"time"
)

// Four real connections per active account. One actor owns both APPEND writers.
const ConnectionsPerAccount = 4

// Close transports before joining tag readers. This never resets debt, delivery
// state, reader cursors, generation counts or the authenticated session.
func (a *mailAccountState) closeAppendWriters(writer **mailPipe, completed chan error, inFlight *int, wait *sync.WaitGroup) bool {
	a.mu.Lock()
	control := a.controlWriter
	companion := control != nil || a.id != (SessionID{})
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
			*inFlight = 0
			*writer = nil
			return companion
		}
	}
}

func (a *mailAccountState) openAppendWriters(attempt context.Context, writer **mailPipe, companion bool) error {
	if a.parking.Load() || a.ctx.Err() != nil {
		return ErrClosed
	}
	// Both logins use one bounded shared startup slot. Do not create a new
	// connection until both old transports have been closed and joined.
	select {
	case a.link.login <- struct{}{}:
	case <-attempt.Done():
		return attempt.Err()
	}
	defer func() { <-a.link.login }()
	bounded, stop := context.WithTimeout(attempt, 15*time.Second)
	defer stop()
	fresh, err := openMailPipeAttempt(bounded, a.ctx, a.config)
	if err != nil {
		return err
	}
	var control *mailPipe
	if companion {
		control, err = openMailPipeAttempt(bounded, a.ctx, a.config)
	}
	if err != nil || a.parking.Load() || a.ctx.Err() != nil {
		fresh.close()
		if control != nil {
			control.close()
		}
		if err != nil {
			return err
		}
		return ErrClosed
	}
	*writer = fresh
	a.mu.Lock()
	a.writer, a.controlWriter = fresh, control
	a.mu.Unlock()
	return nil
}

// Cleanup retains its existing one-attempt behavior and unchanged audit rules.
func (a *mailAccountState) reopenAppendWriters(attempt context.Context, writer **mailPipe, completed chan error, inFlight *int, wait *sync.WaitGroup) {
	companion := a.closeAppendWriters(writer, completed, inFlight, wait)
	_ = a.openAppendWriters(attempt, writer, companion)
}

// Only called at a fence with no uncertain application APPEND. Recover idle
// sockets with bounded, staggered retry, while other accounts continue serving.
func (a *mailAccountState) recoverIdleAppendWriters(writer **mailPipe, completed chan error, inFlight *int, wait *sync.WaitGroup, reason string) bool {
	retryCtx, stopRecovery := context.WithCancel(a.ctx)
	a.mu.Lock()
	a.writerRecoveryStop = stopRecovery
	a.writerRecovering = true
	a.writerRecoveryReason = reason
	a.stage = "writer_reconnecting"
	a.mu.Unlock()
	a.link.notify()
	defer func() {
		stopRecovery()
		a.mu.Lock()
		a.writerRecovering = false
		a.writerRecoveryStop = nil
		a.mu.Unlock()
		a.link.notify()
	}()
	companion := a.closeAppendWriters(writer, completed, inFlight, wait)
	for tries := uint(0); a.ctx.Err() == nil && !a.parking.Load(); tries++ {
		if err := a.openAppendWriters(retryCtx, writer, companion); err == nil {
			a.mu.Lock()
			a.writerReconnects++
			a.stage = "serving"
			a.mu.Unlock()
			return true
		}
		delay := time.Second * time.Duration(1<<min(tries, 3))
		for _, b := range a.config.ID {
			delay += time.Duration(b%17) * time.Millisecond
		}
		timer := time.NewTimer(delay)
		select {
		case <-retryCtx.Done():
			timer.Stop()
			return a.ctx.Err() == nil
		case <-timer.C:
		}
	}
	return a.ctx.Err() == nil
}
