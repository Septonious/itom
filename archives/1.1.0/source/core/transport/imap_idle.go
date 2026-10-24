package transport

import (
	"context"
	"errors"
	"time"
)

func (e *Executor) IdleSupported() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.idleSupported
}

// WaitIdle is bounded notification service, not BODY/discovery/deletion proof.
// It only starts with no pending tags or queued writers. Once started, it owns
// the writer through the continuation, DONE and tagged completion. An ordinary
// command requests DONE immediately, so a quiet mailbox never holds APPEND for
// the audit timer. Caller cancellation drains framing under the existing command
// progress limit rather than closing a healthy socket or abandoning a tag.
func (e *Executor) WaitIdle(ctx context.Context, wake <-chan struct{}, maxWait time.Duration) (err error) {
	if maxWait <= 0 || maxWait > e.limits.ProgressTimeout/2 {
		return ErrCapacity
	}
	e.mu.Lock()
	if e.closed != nil {
		err := e.closed
		e.mu.Unlock()
		return err
	}
	if err := ctx.Err(); err != nil {
		e.mu.Unlock()
		return err
	}
	if !e.idleSupported {
		e.mu.Unlock()
		return ErrRefused
	}
	if len(e.pending) != 0 || e.writerWaiters != 0 {
		e.mu.Unlock()
		return errCommandCapacity
	}
	select {
	case e.writer <- struct{}{}:
	default:
		e.mu.Unlock()
		return errCommandCapacity
	}
	stop := make(chan struct{})
	e.idleStop = stop
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		if errors.Is(err, ErrRefused) {
			e.idleSupported = false
		}
		e.idle = nil
		if e.idleStop == stop {
			e.idleStop = nil
		}
		e.mu.Unlock()
		<-e.writer
	}()
	tag, op, err := e.begin(commandIdle, false, true)
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.idle = op
	e.mu.Unlock()
	if err = e.write([]byte(tag + " IDLE\r\n")); err != nil {
		e.fail(ErrUnknown)
		return ErrUnknown
	}
	// Cancellation/queued publication cannot skip a provider continuation.
	// The independent original command lifetime bounds a silent provider.
	select {
	case <-op.continuation:
	case <-op.done:
		select {
		case <-op.continuation:
			return op.result.Err // legitimate server termination after continuation
		default:
		}
		// Some front ends advertise IDLE then reject it. Keep this selected
		// connection usable and fall back to actual UID discovery, not LOGIN.
		if op.result.Err == nil || errors.Is(op.result.Err, ErrRefused) {
			return ErrRefused
		}
		return op.result.Err
	}
	timer := time.NewTimer(maxWait)
	defer timer.Stop()
	select {
	case <-op.done:
		return op.result.Err
	case <-ctx.Done():
	case <-wake:
	case <-stop:
	case <-e.changes:
	case <-timer.C:
	}
	select {
	case <-op.done:
		return op.result.Err
	default:
	}
	if err = e.write([]byte("DONE\r\n")); err != nil {
		e.fail(ErrUnknown)
		return ErrUnknown
	}
	_, err = (&Command{op}).Wait(context.Background())
	if err != nil {
		return err
	}
	return ctx.Err()
}
