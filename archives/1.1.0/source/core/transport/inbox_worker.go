package transport

import (
	"context"
	"errors"
	"sync"
	"time"
)

// InboxServiceLimits bounds the discovery delay, not an IMAP/server latency
// assumption or a release RTT threshold. Pending/response/body limits remain
// those of the selected executor and Inbox. PreferIdle is intended for quiet
// bulk/standby service; active fast discovery remains independently configured.
type InboxServiceLimits struct {
	MinProbe, MaxProbe time.Duration
	PreferIdle         bool
	IdleAudit          time.Duration
}

func DefaultInboxServiceLimits() InboxServiceLimits {
	return InboxServiceLimits{MinProbe: time.Millisecond, MaxProbe: 25 * time.Millisecond, IdleAudit: 250 * time.Millisecond}
}

// InboxStats reports discovery work. Observed includes repeated inventory UIDs;
// none of these counters is useful delivery, bandwidth or application RTT.
type InboxStats struct {
	Turns, Observed, Applied, Deferred, CapacityWaits uint64
	ServiceTime, ProbeDelay                           time.Duration
	IdleAttempts, IdleRefusals                        uint64
	IdleTime                                          time.Duration
}

// InboxWorker owns independent receive service on an existing selected socket.
// Notifications only shorten a bounded timer: missing/coalesced EXISTS updates
// cannot strand mail. Idle probe delay follows measured empty-command service;
// ready work drains immediately without collecting a batch. No quota, mailbox
// absence or connection replacement authority is created by this worker.
type InboxWorker struct {
	mu      sync.Mutex
	inbox   *Inbox
	limits  InboxServiceLimits
	running bool
	changed chan struct{}
	stats   InboxStats
	runtime *Runtime
}

func NewInboxWorker(inbox *Inbox, limits InboxServiceLimits) (*InboxWorker, error) {
	if inbox == nil || inbox.session == nil || limits.MinProbe <= 0 || limits.MaxProbe < limits.MinProbe || limits.MaxProbe > time.Second {
		return nil, ErrProtocol
	}
	x := inbox.executor
	if limits.PreferIdle && (limits.IdleAudit <= 0 || limits.IdleAudit > time.Second || limits.IdleAudit > x.limits.ProgressTimeout/2) {
		return nil, ErrProtocol
	}
	inbox.session.mu.Lock()
	defer inbox.session.mu.Unlock()
	if inbox.session.runtime != nil || inbox.session.closed {
		return nil, ErrProtocol
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.closed != nil {
		return nil, ErrClosed
	}
	if x.inboxOwner {
		return nil, ErrProtocol
	}
	x.inboxOwner = true
	return &InboxWorker{inbox: inbox, limits: limits, changed: make(chan struct{}), stats: InboxStats{ProbeDelay: limits.MinProbe}}, nil
}

func (w *InboxWorker) Stats() InboxStats { w.mu.Lock(); defer w.mu.Unlock(); return w.stats }
func (w *InboxWorker) Changes() <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.changed
}
func (w *InboxWorker) signalLocked() { close(w.changed); w.changed = make(chan struct{}) }

// Run does not close the executor on cancellation. An issued FETCH still drains
// under its original progress timeout, and a later Run waits for its tag before
// issuing another. Only the runtime owner may finally shut down the carrier.
// Once a worker is created, other callers must stop invoking Poll/Sweep directly.
func (w *InboxWorker) Run(ctx context.Context) error {
	return w.run(ctx, nil)
}

func (w *InboxWorker) run(ctx context.Context, runtime *Runtime) error {
	w.inbox.session.mu.Lock()
	if w.inbox.session.closed || w.inbox.session.runtime != runtime {
		w.inbox.session.mu.Unlock()
		return ErrProtocol
	}
	w.mu.Lock()
	if w.running || w.runtime != runtime {
		w.mu.Unlock()
		w.inbox.session.mu.Unlock()
		return ErrProtocol
	}
	w.running = true
	w.inbox.session.services++
	w.mu.Unlock()
	w.inbox.session.mu.Unlock()
	if runtime != nil { runtime.serviceStarted(w.inbox.executor, false) }
	defer func() {
		w.mu.Lock()
		w.running = false
		w.signalLocked()
		w.mu.Unlock()
		w.inbox.session.mu.Lock()
		w.inbox.session.services--
		w.inbox.session.mu.Unlock()
	}()
	x, s := w.inbox.executor, w.inbox.session
	delay := w.limits.MinProbe
	var emptyService time.Duration
	stagnant := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.Snapshot().Closed {
			return ErrClosed
		}
		work, space := s.WorkChanged(), x.SpaceChanged()
		begin := time.Now()
		p, err := w.inbox.Poll(ctx)
		elapsed := time.Since(begin)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, errCommandCapacity) {
			x.mu.Lock()
			blocked := x.fetch != nil || len(x.pending) >= x.limits.Pending
			x.mu.Unlock()
			if blocked {
				w.mu.Lock()
				w.stats.CapacityWaits++
				w.signalLocked()
				w.mu.Unlock()
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-x.done:
					return ErrClosed
				case <-work:
					continue
				case <-space:
					continue
				}
			}
			// A tag can drain between the failed command admission and the
			// capacity snapshot. Its sampled edge still permits a safe retry.
			select {
			case <-space:
				continue
			default:
			}
		}
		if err != nil {
			return err
		}
		// Inventory may enumerate old, already-seen UIDs while discovering no
		// new work. Those turns still measure idle service, not bulk demand.
		if p.Applied == 0 && p.Deferred == 0 && !p.More {
			if emptyService == 0 {
				emptyService = elapsed
			} else {
				emptyService = emptyService*3/4 + elapsed/4
			}
			delay = max(w.limits.MinProbe, min(w.limits.MaxProbe, emptyService/8))
		}
		w.mu.Lock()
		w.stats.Turns++
		w.stats.Observed += uint64(p.Observed)
		w.stats.Applied += uint64(p.Applied)
		w.stats.Deferred += uint64(p.Deferred)
		w.stats.ServiceTime += elapsed
		w.stats.ProbeDelay = delay
		w.signalLocked()
		w.mu.Unlock()
		if p.Applied > 0 {
			stagnant = 0
			continue
		}
		if p.More {
			stagnant++
			if stagnant < maxBlockedInboxProbes {
				continue
			}
		}
		stagnant = 0
		if w.limits.PreferIdle && p.Deferred == 0 && !p.More && x.IdleSupported() {
			idleSpace := x.SpaceChanged()
			begin := time.Now()
			err := x.WaitIdle(ctx, work, w.limits.IdleAudit)
			w.mu.Lock()
			w.stats.IdleAttempts++
			w.stats.IdleTime += time.Since(begin)
			if errors.Is(err, ErrRefused) {
				w.stats.IdleRefusals++
			}
			w.signalLocked()
			w.mu.Unlock()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err == nil {
				continue
			}
			if errors.Is(err, errCommandCapacity) {
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-x.done:
					timer.Stop()
					return ErrClosed
				case <-idleSpace:
				case <-work:
				case <-timer.C:
				}
				timer.Stop()
				continue
			}
			if !errors.Is(err, ErrRefused) {
				return err
			}
			// Complete rejection disables IDLE only on this selected executor.
			// Normal bounded UID discovery below preserves missing-hint liveness.
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-x.done:
			timer.Stop()
			return ErrClosed
		case <-x.Changes():
		case <-work:
		case <-timer.C:
		}
		timer.Stop()
	}
}
