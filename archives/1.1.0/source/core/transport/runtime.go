package transport

import (
	"context"
	"errors"
	"sync"

	"itom.local/core/journal"
)

var ErrRuntimeStopped = errors.New("V2_RUNTIME_SERVICE_STOPPED")

// RuntimeBudget is supplied by the authenticated configuration/roster owner.
// No connection is created here, and available budget is never inferred from
// the number of lab workers. The primary phone/server per-account budgets are
// two/three respectively; explicitly negotiated extra service remains bounded.
type RuntimeBudget struct{ Connections, PerAccount int }

type RuntimeCarrier struct {
	Reader *InboxWorker
	Writer *PublicationWorker
}

type ServicePhase string

const (
	ServicePrepared ServicePhase = "prepared"
	ServiceServing  ServicePhase = "serving"
	ServiceStopped  ServicePhase = "stopped"
	ServiceFailed   ServicePhase = "failed"
	ServiceClosed   ServicePhase = "closed"
)

type CarrierServiceSnapshot struct {
	Account                   string
	Plane                     Plane
	UIDValidity               uint32
	Read, Write               ServicePhase
	ReadFailure, WriteFailure string
	Receive                   InboxStats
	Publish                   PublicationStats
	IMAP                      ExecutorSnapshot
}

// Serving reports local worker execution only. It is NOT authenticated READY,
// VPN CONNECTED, complete peer permission, or release/performance qualification.
type RuntimeSnapshot struct {
	Phase    ServicePhase
	Exit     ServicePhase
	Session  SessionSnapshot
	Carriers []CarrierServiceSnapshot
}

// Runtime owns a single authenticated logical session's already scoped carrier
// services. Journals/wallets are borrowed and never closed or erased here. A
// failed write leaves reception available for authenticated progress/recovery;
// a failed reader stops new writes on that carrier while other carriers live.
// There is no blind resend, account replacement, permission refill or retry.
type Runtime struct {
	mu        sync.Mutex
	session   *Session
	exit      *ExitServer
	carriers  []RuntimeCarrier
	status    []CarrierServiceSnapshot
	indices   map[*Executor]int
	phase     ServicePhase
	exitPhase ServicePhase
	started   bool
	cancel    context.CancelFunc
	changed   chan struct{}
}

func NewRuntime(session *Session, exit *ExitServer, budget RuntimeBudget, carriers ...RuntimeCarrier) (*Runtime, error) {
	if session == nil || budget.Connections < 2 || budget.Connections > 4096 || budget.PerAccount < 2 || budget.PerAccount > 128 || len(carriers) < 2 || len(carriers) > budget.Connections {
		return nil, ErrProtocol
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return nil, ErrClosed
	}
	if session.crypto == nil || session.runtime != nil || session.services != 0 || session.openRole == 0 || (session.openRole == 1) != (exit == nil) || exit != nil && exit.session != session {
		return nil, ErrProtocol
	}
	seen := make(map[*Executor]bool)
	type scope struct {
		folder   string
		validity uint32
	}
	type account struct {
		count  int
		planes map[Plane]scope
	}
	accounts := make(map[string]*account)
	for _, c := range carriers {
		if c.Reader == nil || c.Writer == nil || c.Reader.inbox == nil {
			return nil, ErrProtocol
		}
		i, w := c.Reader.inbox, c.Writer
		x := i.executor
		if i.session != session || w.session != session || w.executor != x || seen[x] || w.plane > Bulk || len(i.routes) != len(w.sources) {
			return nil, ErrProtocol
		}
		seen[x] = true
		classes := make(map[LedgerClass]bool)
		for _, source := range w.sources {
			if classes[source.class] || i.routes[source.class] != source.store {
				return nil, ErrProtocol
			}
			classes[source.class] = true
		}
		if !classes[DataLedger] || (w.plane == Fast) != classes[ControlLedger] {
			return nil, ErrProtocol
		}
		x.mu.Lock()
		closed := x.closed != nil
		x.mu.Unlock()
		if closed {
			return nil, ErrClosed
		}
		a := accounts[x.account]
		if a == nil {
			a = &account{planes: make(map[Plane]scope)}
			accounts[x.account] = a
		}
		a.count++
		if a.count > budget.PerAccount {
			return nil, ErrProtocol
		}
		current := scope{x.mailbox, x.validity}
		if prior, ok := a.planes[w.plane]; ok && (w.plane == Fast || prior != current) {
			return nil, ErrProtocol
		}
		a.planes[w.plane] = current
	}
	for _, a := range accounts {
		fast, f := a.planes[Fast]
		bulk, b := a.planes[Bulk]
		if !f || !b || fast.folder == bulk.folder {
			return nil, ErrProtocol
		}
	}
	// Lock every actor before claiming any, so invalid or concurrently running
	// inputs cannot leave a partly owned runtime. All share this Session lock;
	// independent worker startup never holds its lock across a Session call.
	for _, c := range carriers {
		c.Reader.mu.Lock()
		c.Writer.mu.Lock()
	}
	defer func() {
		for i := len(carriers) - 1; i >= 0; i-- {
			carriers[i].Writer.mu.Unlock()
			carriers[i].Reader.mu.Unlock()
		}
	}()
	if exit != nil {
		exit.mu.Lock()
		defer exit.mu.Unlock()
		if exit.running || exit.runtime != nil {
			return nil, ErrProtocol
		}
	}
	for _, c := range carriers {
		if c.Reader.running || c.Writer.running || c.Reader.runtime != nil || c.Writer.runtime != nil {
			return nil, ErrProtocol
		}
	}
	e := &Runtime{session: session, exit: exit, carriers: append([]RuntimeCarrier(nil), carriers...), indices: make(map[*Executor]int, len(carriers)), phase: ServicePrepared, exitPhase: ServiceStopped, changed: make(chan struct{})}
	for _, c := range carriers {
		x := c.Writer.executor
		e.indices[x] = len(e.status)
		e.status = append(e.status, CarrierServiceSnapshot{Account: x.account, Plane: c.Writer.plane, UIDValidity: x.validity, Read: ServicePrepared, Write: ServicePrepared})
		c.Reader.runtime, c.Writer.runtime = e, e
	}
	if exit != nil {
		exit.runtime = e
		e.exitPhase = ServicePrepared
	}
	session.runtime = e
	return e, nil
}

func (e *Runtime) signalLocked()            { close(e.changed); e.changed = make(chan struct{}) }
func (e *Runtime) Changes() <-chan struct{} { e.mu.Lock(); defer e.mu.Unlock(); return e.changed }
func (e *Runtime) Snapshot() RuntimeSnapshot {
	e.mu.Lock()
	snapshot := RuntimeSnapshot{Phase: e.phase, Exit: e.exitPhase, Carriers: append([]CarrierServiceSnapshot(nil), e.status...)}
	e.mu.Unlock()
	snapshot.Session = e.session.Snapshot()
	for i, c := range e.carriers {
		snapshot.Carriers[i].Receive = c.Reader.Stats()
		snapshot.Carriers[i].Publish = c.Writer.Stats()
		snapshot.Carriers[i].IMAP = c.Writer.executor.Snapshot()
	}
	return snapshot
}

func (e *Runtime) serviceStarted(executor *Executor, write bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.phase != ServiceServing {
		return
	}
	if executor == nil {
		e.exitPhase = ServiceServing
	} else {
		i, exists := e.indices[executor]
		if !exists {
			return
		}
		if write {
			e.status[i].Write = ServiceServing
		} else {
			e.status[i].Read = ServiceServing
		}
	}
	e.signalLocked()
}

// PendingPublication is the exact original body for an explicit recovery owner.
// It grants no permission to resend an uncertain body, refund or infer delivery.
func (e *Runtime) PendingPublication(index int) *PreparedPublication {
	if index < 0 || index >= len(e.carriers) {
		return nil
	}
	return e.carriers[index].Writer.Pending()
}

func serviceFailure(err error) string {
	for _, known := range []error{journal.ErrScope, journal.ErrLimit, journal.ErrState, journal.ErrMissing, journal.ErrGeneration, journal.ErrCorrupt, journal.ErrPoisoned} {
		if errors.Is(err, known) {
			return known.Error()
		}
	}
	switch {
	case errors.Is(err, ErrProtocol):
		return "protocol"
	case errors.Is(err, ErrCapacity):
		return "capacity"
	case errors.Is(err, ErrUnknown), errors.Is(err, ErrClosed):
		return "carrier"
	default:
		return "service"
	}
}

// Run starts the already authenticated/scoped components once. It cannot turn
// fixture-supplied keys/grants into a production bootstrap or READY negotiation.
// Cancellation stops admission and joins issued commands under their original
// progress limits. Close is the final VPN/runtime-owner shutdown boundary.
func (e *Runtime) Run(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	if e.started || e.phase == ServiceClosed {
		e.mu.Unlock()
		return ErrProtocol
	}
	ctx, cancel := context.WithCancel(ctx)
	e.started, e.cancel, e.phase = true, cancel, ServiceServing
	e.signalLocked()
	e.mu.Unlock()
	type result struct {
		index       int
		write, exit bool
		err         error
	}
	results := make(chan result, 2*len(e.carriers)+1)
	var workers sync.WaitGroup
	writeCancel := make([]context.CancelFunc, len(e.carriers))
	for i, c := range e.carriers {
		writerCtx, stop := context.WithCancel(ctx)
		writeCancel[i] = stop
		workers.Go(func() { results <- result{index: i, write: true, err: c.Writer.run(writerCtx, e)} })
		workers.Go(func() { results <- result{index: i, err: c.Reader.run(ctx, e)} })
	}
	if e.exit != nil {
		workers.Go(func() { results <- result{exit: true, err: e.exit.run(ctx, e)} })
	}
	finish := func(phase ServicePhase) {
		cancel()
		workers.Wait()
		e.mu.Lock()
		e.cancel = nil
		if e.phase != ServiceClosed {
			e.phase = phase
			if e.exit != nil {
				e.exitPhase = ServiceStopped
			}
		}
		for i := range e.status {
			if e.status[i].Read == ServiceServing || e.status[i].Read == ServicePrepared {
				e.status[i].Read = ServiceStopped
			}
			if e.status[i].Write == ServiceServing || e.status[i].Write == ServicePrepared {
				e.status[i].Write = ServiceStopped
			}
		}
		e.signalLocked()
		e.mu.Unlock()
	}
	defer func() {
		for _, stop := range writeCancel {
			stop()
		}
	}()
	remaining := 2 * len(e.carriers)
	for remaining > 0 {
		select {
		case <-ctx.Done():
			finish(ServiceStopped)
			return ctx.Err()
		case r := <-results:
			if r.exit {
				if ctx.Err() != nil {
					finish(ServiceStopped)
					return ctx.Err()
				}
				finish(ServiceFailed)
				return ErrRuntimeStopped
			}
			remaining--
			phase, reason := ServiceStopped, ""
			if r.err != nil && !errors.Is(r.err, context.Canceled) {
				phase, reason = ServiceFailed, serviceFailure(r.err)
			}
			e.mu.Lock()
			if e.phase == ServiceClosed {
				phase = ServiceClosed
				if ctx.Err() != nil {
					reason = ""
				}
			}
			if r.write {
				e.status[r.index].Write, e.status[r.index].WriteFailure = phase, reason
			} else {
				e.status[r.index].Read, e.status[r.index].ReadFailure = phase, reason
			}
			e.signalLocked()
			e.mu.Unlock()
			if !r.write {
				writeCancel[r.index]()
			}
		}
	}
	if ctx.Err() != nil {
		finish(ServiceStopped)
		return ctx.Err()
	}
	finish(ServiceFailed)
	return ErrRuntimeStopped
}

func (e *Runtime) Close() error {
	e.mu.Lock()
	if e.phase == ServiceClosed {
		e.mu.Unlock()
		return nil
	}
	e.phase = ServiceClosed
	if e.exit != nil {
		e.exitPhase = ServiceClosed
	}
	for i := range e.status {
		e.status[i].Read, e.status[i].Write = ServiceClosed, ServiceClosed
	}
	if e.cancel != nil {
		e.cancel()
	}
	e.signalLocked()
	e.mu.Unlock()
	e.session.Close()
	for _, c := range e.carriers {
		c.Writer.executor.Close()
	}
	return nil
}
