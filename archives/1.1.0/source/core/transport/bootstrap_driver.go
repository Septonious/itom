package transport

import (
	"context"
	"errors"
	"sync"
	"time"
)

// BootstrapEvent records only a fixed stage code and local monotonic timings.
// It never contains mail bodies, profile/target strings, keys or provider errors.
type BootstrapEvent struct {
	Stage      string `json:"stage"`
	AtNS       int64  `json:"at_ns"`
	DurationNS int64  `json:"duration_ns"`
}

type BootstrapSnapshot struct {
	Phase           ServicePhase     `json:"phase"`
	Client          bool             `json:"client"`
	Failure         string           `json:"failure,omitempty"`
	Prepared        uint64           `json:"prepared"`
	Accepted        uint64           `json:"accepted"`
	Uncertain       uint64           `json:"uncertain"`
	DiscoveryTurns  uint64           `json:"discovery_turns"`
	OwnObserved     int              `json:"own_observed"`
	PeerObserved    int              `json:"peer_observed"`
	HandoffComplete bool             `json:"handoff_complete"`
	PrepareNS       int64            `json:"prepare_ns"`
	AppendNS        int64            `json:"append_ns"`
	DiscoveryNS     int64            `json:"discovery_ns"`
	WaitNS          int64            `json:"wait_ns"`
	ElapsedNS       int64            `json:"elapsed_ns"`
	Events          []BootstrapEvent `json:"events"`
	EventsDropped   uint64           `json:"events_dropped"`
	IMAP            ExecutorSnapshot `json:"imap"`
}

// BootstrapDriver owns one actual selected fast executor until key establishment
// and complete BODY observation allow handover. It supplies neither the initial
// grant nor READY/roster negotiation. No extra connection or timeout-based write
// permission is created. Run cancellation retains ownership/state for a later
// Run on this same object; Close is the explicit failed-start shutdown boundary.
type BootstrapDriver struct {
	mu       sync.Mutex
	owner    *SessionBootstrap
	executor *Executor
	inbox    *Inbox
	limits   InboxServiceLimits
	running  bool
	closed   bool
	started  time.Time
	ended    time.Time
	status   BootstrapSnapshot
	handoff  *BootstrapHandoff
	prepared *PreparedPublication
}

func NewBootstrapDriver(owner *SessionBootstrap, executor *Executor, limits InboxServiceLimits) (*BootstrapDriver, error) {
	if owner == nil || executor == nil || limits.MinProbe <= 0 || limits.MaxProbe < limits.MinProbe || limits.MaxProbe > time.Second || limits.PreferIdle {
		return nil, ErrProtocol
	}
	d := &BootstrapDriver{owner: owner, executor: executor, limits: limits, status: BootstrapSnapshot{Phase: ServicePrepared}}
	owner.mu.Lock()
	if owner.driver != nil || owner.closed || owner.failed || owner.handedOff || executor.account != owner.origin.Account || executor.mailbox != owner.origin.Folder || executor.validity != owner.origin.UIDValidity {
		owner.mu.Unlock()
		return nil, ErrProtocol
	}
	executor.mu.Lock()
	if executor.closed != nil || executor.bootstrapOwner || executor.publicationOwner || executor.inboxOwner || len(executor.pending) != 0 {
		executor.mu.Unlock()
		owner.mu.Unlock()
		return nil, ErrProtocol
	}
	owner.driver, executor.bootstrapOwner = d, true
	d.status.Client = owner.client
	executor.mu.Unlock()
	owner.mu.Unlock()
	i, err := owner.inbox(executor, DefaultInboxLimits(), d)
	if err != nil {
		// No operation has started, so failed construction may roll back its
		// ownership claim without changing grants, keys or pending commands.
		owner.mu.Lock()
		executor.mu.Lock()
		owner.driver, executor.bootstrapOwner = nil, false
		executor.mu.Unlock()
		owner.mu.Unlock()
		return nil, err
	}
	d.inbox = i
	return d, nil
}

func (d *BootstrapDriver) Snapshot() BootstrapSnapshot {
	d.mu.Lock()
	s := d.status
	s.Events = append([]BootstrapEvent(nil), s.Events...)
	if !d.started.IsZero() {
		end := d.ended
		if end.IsZero() {
			end = time.Now()
		}
		s.ElapsedNS = end.Sub(d.started).Nanoseconds()
	}
	d.mu.Unlock()
	d.owner.mu.Lock()
	for index, seen := range d.owner.observed {
		if seen {
			if (index%2 == 0) == d.owner.client {
				s.OwnObserved++
			} else {
				s.PeerObserved++
			}
		}
	}
	d.owner.mu.Unlock()
	s.IMAP = d.executor.Snapshot()
	return s
}

func (d *BootstrapDriver) record(stage string, begin time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	elapsed := time.Since(begin).Nanoseconds()
	switch stage {
	case "prepare":
		d.status.PrepareNS += elapsed
	case "append":
		d.status.AppendNS += elapsed
	case "discover":
		d.status.DiscoveryNS += elapsed
		d.status.DiscoveryTurns++
	case "wait":
		d.status.WaitNS += elapsed
	}
	event := BootstrapEvent{Stage: stage, AtNS: begin.Sub(d.started).Nanoseconds(), DurationNS: elapsed}
	if len(d.status.Events) == 64 {
		copy(d.status.Events, d.status.Events[1:])
		d.status.Events[63] = event
		d.status.EventsDropped++
	} else {
		d.status.Events = append(d.status.Events, event)
	}
}

func bootstrapFailure(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, ErrUnknown):
		return "unknown_write"
	case permissionUnavailable(err):
		return "permission"
	case errors.Is(err, ErrProtocol):
		return "protocol"
	case errors.Is(err, ErrCapacity):
		return "capacity"
	case errors.Is(err, ErrClosed):
		return "closed"
	default:
		return "service"
	}
}

func (d *BootstrapDriver) wait(ctx context.Context, space <-chan struct{}, delay time.Duration) error {
	begin := time.Now()
	defer d.record("wait", begin)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-d.executor.done:
		return ErrClosed
	case <-d.executor.Changes():
	case <-space:
	case <-timer.C:
	}
	return nil
}

// Run advances only from actual command outcomes and authenticated peer bodies.
// Missing permission returns to the authority owner rather than minting/refilling
// a grant. Unknown APPEND returns with its exact Pending publication retained;
// an explicit proof owner can resolve it before a subsequent Run.
func (d *BootstrapDriver) Run(ctx context.Context) (handoff *BootstrapHandoff, result error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.Lock()
	if d.closed || d.running {
		d.mu.Unlock()
		return nil, ErrProtocol
	}
	if d.handoff != nil {
		h := d.handoff
		d.mu.Unlock()
		return h, nil
	}
	if d.started.IsZero() {
		d.started = time.Now()
	}
	d.ended = time.Time{}
	d.running, d.status.Phase, d.status.Failure = true, ServiceServing, ""
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.running = false
		d.ended = time.Now()
		if !d.closed && handoff == nil {
			d.status.Phase = ServiceFailed
			if errors.Is(result, context.Canceled) || errors.Is(result, context.DeadlineExceeded) {
				d.status.Phase = ServiceStopped
			}
			d.status.Failure = bootstrapFailure(result)
		}
		d.mu.Unlock()
	}()
	var emptyService time.Duration
	delay := d.limits.MinProbe
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if h, err := d.owner.handoff(d); err == nil {
			d.mu.Lock()
			if d.closed {
				d.mu.Unlock()
				return nil, ErrClosed
			}
			d.handoff, d.status.Phase, d.status.HandoffComplete = h, ServiceStopped, true
			// Close cannot close a successfully transferred runtime socket.
			d.executor.mu.Lock()
			d.executor.bootstrapOwner = false
			d.executor.mu.Unlock()
			d.mu.Unlock()
			return h, nil
		} else if !errors.Is(err, ErrBootstrapPending) {
			return nil, err
		}
		space := d.executor.SpaceChanged()
		begin := time.Now()
		p, err := d.owner.prepare(ctx, d)
		d.record("prepare", begin)
		if err != nil && !errors.Is(err, ErrBootstrapPending) {
			return nil, err
		}
		if p != nil {
			d.mu.Lock()
			if d.prepared != p {
				d.prepared = p
				d.status.Prepared++
			}
			d.mu.Unlock()
			begin = time.Now()
			outcome, err := p.Start(ctx, d.executor)
			d.record("append", begin)
			d.mu.Lock()
			if outcome == Accepted {
				d.status.Accepted++
			}
			if outcome == Uncertain {
				d.status.Uncertain++
			}
			d.mu.Unlock()
			if outcome == NotStarted && errors.Is(err, errCommandCapacity) {
				if err = d.wait(ctx, space, delay); err != nil {
					return nil, err
				}
				continue
			}
			if outcome == Uncertain {
				return nil, errors.Join(ErrUnknown, err)
			}
			if err != nil || outcome != Accepted {
				return nil, errors.Join(ErrProtocol, err)
			}
		}
		space = d.executor.SpaceChanged()
		begin = time.Now()
		progress, err := d.inbox.Poll(ctx)
		elapsed := time.Since(begin)
		d.record("discover", begin)
		if err != nil && !errors.Is(err, errCommandCapacity) {
			return nil, err
		}
		if err == nil && progress.Applied > 0 {
			continue
		}
		if err == nil && !progress.More && progress.Deferred == 0 {
			if emptyService == 0 {
				emptyService = elapsed
			} else {
				emptyService = emptyService*3/4 + elapsed/4
			}
			delay = max(d.limits.MinProbe, min(d.limits.MaxProbe, emptyService/8))
		}
		if err = d.wait(ctx, space, delay); err != nil {
			return nil, err
		}
	}
}

// Close never closes borrowed wallets/journals. Before successful handover it
// closes this driver's executor; after handover that socket belongs to Runtime.
func (d *BootstrapDriver) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed, d.status.Phase = true, ServiceClosed
	handedOff := d.handoff != nil
	d.mu.Unlock()
	d.owner.Close()
	if !handedOff {
		d.executor.Close()
	}
	return nil
}
