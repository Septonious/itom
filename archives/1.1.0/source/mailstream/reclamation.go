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
	// Only real control-byte pressure can donate a ready DATA collection turn.
	// Historical folder sweeps must not starve the exact current-session
	// collection that is already able to release physical capacity.
	a.mu.Lock()
	maintenance := a.maintenance.requested && max(a.maintenance.controlBytes, a.maintenance.incomingBytes) >= reservationQuantum/2
	if maintenance {
		job.record.queued = false
	}
	a.mu.Unlock()
	if maintenance {
		a.processMaintenance(writer, completed, inFlight, wait)
		return
	}
	unqueue := func(refused bool) {
		a.mu.Lock()
		job.record.queued = false
		job.record.refused = refused
		a.mu.Unlock()
	}
	if !c.permitted(a, job.record.folder) {
		unqueue(true)
		return
	}
	if *writer == nil || a.parking.Load() {
		unqueue(false)
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
	// Drain this account's ordinary APPEND tags before taking the shared
	// mutation lease. Other ready accounts can collect during this wait.
	// No mailbox mutation or capacity refund has occurred yet.
	leased := false
	if err == nil && drained {
		if !c.acquire(a) {
			unqueue(false)
			return
		}
		leased = true
		defer c.release()
		if !c.permitted(a, job.record.folder) {
			unqueue(true)
			return
		}
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
		c.completed(job.record.folder)
		return
	}
	// A cleanup refusal/timeout is not a carrier failure or deletion proof.
	// Stop trying other accounts, preserve debt, and restore only this writer.
	if leased && !a.parking.Load() && a.ctx.Err() == nil {
		reason := "CLEANUP_FAILED"
		if !stopped {
			reason = "CLEANUP_TIMEOUT"
		}
		c.pause(reason, a)
	}
	unqueue(leased)
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
