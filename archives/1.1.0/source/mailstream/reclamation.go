package mailstream

import (
	"context"
	"sync"
	"time"
)

// Runs only on the account's APPEND actor. Receivers and the session control
// owner never wait for this optional operation. Parking preempts its raw IO.
func (a *mailAccountState) processCleanup(job cleanupJob, writer **mailPipe, completed chan error, inFlight *int, wait *sync.WaitGroup) {
	c := a.link.reclaimer
	unqueue := func(refused bool) {
		a.mu.Lock()
		job.record.queued = false
		job.record.refused = refused
		a.mu.Unlock()
	}
	if c.snapshot().Paused {
		unqueue(true)
		return
	}
	if *writer == nil || a.parking.Load() {
		unqueue(false)
		return
	}
	if !c.acquire(a) {
		unqueue(false)
		return
	}
	defer c.release()
	// Another identity may have opened the circuit while this owner waited.
	if c.snapshot().Paused {
		unqueue(true)
		return
	}
	a.reclaiming.Store(true)
	defer a.reclaiming.Store(false)
	a.link.notify()
	p := *writer
	budget := min(10*time.Second, max(2*time.Second, 8*p.service))
	// In-flight bulk completion is not cleanup service time. On a weak
	// shared link it may legitimately take longer than the metadata bound.
	stop := p.limit(10 * time.Second)
	c.stage("drain")
	var err error
	for *inFlight > 0 && err == nil {
		select {
		case err = <-completed:
			*inFlight--
		case <-a.ctx.Done():
			err = a.ctx.Err()
		}
	}
	drained := stop()
	if err == nil && !drained {
		err = context.DeadlineExceeded
	}
	stop = p.limit(budget)
	if err == nil && !a.parking.Load() {
		err = p.reclaim(job, c)
	} else if err == nil {
		err = context.Canceled
	}
	stopped := stop() && drained
	if err == nil && stopped {
		err = a.finishCollection(job)
	}
	if err == nil && stopped {
		c.completed()
		return
	}
	// A cleanup refusal/timeout is not a carrier failure or deletion proof.
	// Stop trying other accounts, preserve debt, and restore only this writer.
	if !a.parking.Load() && a.ctx.Err() == nil {
		reason := "CLEANUP_FAILED"
		if !stopped {
			reason = "CLEANUP_TIMEOUT"
		}
		c.pause(reason)
	}
	unqueue(true)
	p.close()
	wait.Wait()
	for {
		select {
		case <-completed:
		default:
			goto completionsDrained
		}
	}
completionsDrained:
	*inFlight = 0
	*writer = nil
	a.mu.Lock()
	a.writer = nil
	a.mu.Unlock()
	if !a.parking.Load() && a.ctx.Err() == nil {
		fresh, openErr := openMailPipe(a.ctx, a.config)
		if openErr == nil {
			*writer = fresh
			a.mu.Lock()
			a.writer = fresh
			a.mu.Unlock()
		}
	}
	a.link.notify()
}
