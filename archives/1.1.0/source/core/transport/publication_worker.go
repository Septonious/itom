package transport

import (
	"context"
	"errors"
	"sync"
	"time"

	"itom.local/core/journal"
	"itom.local/core/wallet"
	"itom.local/core/wire"
)

type publicationSource struct {
	class   LedgerClass
	store   wire.ID
	prepare func(context.Context) (*PreparedPublication, error)
}

// PublicationStats describes physical service only. Accepted bytes and own
// storage proof are never samples of useful peer delivery or application RTT.
type PublicationStats struct {
	LastAcceptedKindsAtMS                          [12]int64
	PreparedKinds, AcceptedKinds                   [12]uint64
	Prepared, Accepted, Uncertain, ConfirmedStored uint64
	AcceptedEncodedBytes                           uint64
	PrepareTime, PublishTime                       time.Duration
	DataBlocked, ControlBlocked                    bool
}

// PublicationWorker owns one selected executor's publisher turn. It performs
// neither mailbox reads nor session-lock I/O, so an independent bounded inbox
// read can finish while APPEND waits for its tag. It creates no connections.
// There is at most one exact prepared body retained by this worker; uncertainty
// stops writes for explicit recovery and can never become an automatic retry.
type PublicationWorker struct {
	mu          sync.Mutex
	session     *Session
	executor    *Executor
	plane       Plane
	sources     []publicationSource
	cursor      int
	running     bool
	pending     *PreparedPublication
	permissions chan struct{}
	changed     chan struct{}
	epoch       uint64
	stats       PublicationStats
	runtime     *Runtime
}

func NewClientPublicationWorker(executor *Executor, plane Plane, publishers ...*ClientPublisher) (*PublicationWorker, error) {
	if len(publishers) < 1 || len(publishers) > 2 || publishers[0] == nil {
		return nil, ErrProtocol
	}
	first := publishers[0]
	classes := map[LedgerClass]bool{}
	stores := map[wire.ID]bool{}
	var sources []publicationSource
	for _, p := range publishers {
		if p == nil || p.plane != plane || p.session != first.session || p.cipher != first.cipher || p.account != first.account || p.folder != first.folder || p.authenticationKey != first.authenticationKey || classes[p.class] || stores[p.wallet.Identity().Store] {
			return nil, ErrProtocol
		}
		classes[p.class] = true
		stores[p.wallet.Identity().Store] = true
		prepare := func(ctx context.Context) (*PreparedPublication, error) {
			if p.class == ControlLedger {
				return p.PrepareControls(ctx, MaxControlBytes)
			}
			if plane == Bulk {
				return p.PrepareBulk(ctx, MaxBulkBytes)
			}
			return p.Prepare(ctx, MaxPayload)
		}
		sources = append(sources, publicationSource{class: p.class, store: p.wallet.Identity().Store, prepare: prepare})
	}
	return newPublicationWorker(executor, plane, first.session, first.account, first.folder, sources)
}

func NewServerPublicationWorker(executor *Executor, plane Plane, publishers ...*ServerPublisher) (*PublicationWorker, error) {
	if len(publishers) < 1 || len(publishers) > 2 || publishers[0] == nil {
		return nil, ErrProtocol
	}
	first := publishers[0]
	classes := map[LedgerClass]bool{}
	stores := map[wire.ID]bool{}
	var sources []publicationSource
	for _, p := range publishers {
		if p == nil || p.plane != plane || p.session != first.session || p.cipher != first.cipher || p.scope != first.scope || p.folder != first.folder || p.authenticationKey != first.authenticationKey || classes[p.class] || stores[p.store.StoreID()] {
			return nil, ErrProtocol
		}
		classes[p.class] = true
		stores[p.store.StoreID()] = true
		prepare := func(ctx context.Context) (*PreparedPublication, error) {
			if p.class == ControlLedger {
				return p.PrepareControls(ctx, MaxControlBytes)
			}
			if plane == Bulk {
				return p.PrepareBulk(ctx, MaxBulkBytes)
			}
			return p.Prepare(ctx, MaxPayload)
		}
		sources = append(sources, publicationSource{class: p.class, store: p.store.StoreID(), prepare: prepare})
	}
	return newPublicationWorker(executor, plane, first.session, first.scope.Account, first.folder, sources)
}

func newPublicationWorker(x *Executor, plane Plane, s *Session, account, folder string, sources []publicationSource) (*PublicationWorker, error) {
	if x == nil || plane > Bulk || x.account == "" || x.account != account || x.mailbox != folder || x.validity == 0 {
		return nil, ErrProtocol
	}
	// Protocol gets the initial turn, then ready ledgers alternate. A continuous
	// stream of receipts cannot permanently starve small DATA on the fast path.
	if len(sources) == 2 && sources[1].class == ControlLedger {
		sources[0], sources[1] = sources[1], sources[0]
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runtime != nil || s.closed {
		return nil, ErrProtocol
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.closed != nil {
		return nil, ErrClosed
	}
	if x.publicationOwner || x.bootstrapOwner {
		return nil, ErrProtocol
	}
	x.publicationOwner = true
	return &PublicationWorker{session: s, executor: x, plane: plane, sources: sources, permissions: make(chan struct{}), changed: make(chan struct{})}, nil
}

// NotifyPermission follows an actual grant/reclamation/recovery state update.
// A timeout never creates permission, clears debt or erases an exhausted grant.
func (w *PublicationWorker) NotifyPermission() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.epoch++
	close(w.permissions)
	w.permissions = make(chan struct{})
	w.signalLocked()
}
func (w *PublicationWorker) signalLocked() { close(w.changed); w.changed = make(chan struct{}) }
func (w *PublicationWorker) Changes() <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.changed
}
func (w *PublicationWorker) Stats() PublicationStats {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stats
}

// Pending is the worker's single exact charged body. The recovery owner may
// ConfirmStored; it must never call Start concurrently with Run or re-APPEND an
// uncertain body. A definitely unstarted body can resume on a later Run.
func (w *PublicationWorker) Pending() *PreparedPublication {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.pending
}

func permissionUnavailable(err error) bool {
	return errors.Is(err, wallet.ErrExhausted) || errors.Is(err, wallet.ErrLimit) || errors.Is(err, journal.ErrLimit) || errors.Is(err, journal.ErrRecovery)
}

// Run cancellation stops new admission. A command already issued still drains
// under the executor's unchanged progress limit; application cancellation must
// not close its socket. The session/connection owner controls final shutdown.
func (w *PublicationWorker) Run(ctx context.Context) error {
	return w.run(ctx, nil)
}

func (w *PublicationWorker) run(ctx context.Context, runtime *Runtime) error {
	w.session.mu.Lock()
	if w.session.closed || w.session.runtime != runtime {
		w.session.mu.Unlock()
		return ErrProtocol
	}
	w.mu.Lock()
	if w.running || w.runtime != runtime {
		w.mu.Unlock()
		w.session.mu.Unlock()
		return ErrProtocol
	}
	w.running = true
	w.session.services++
	w.signalLocked()
	w.mu.Unlock()
	w.session.mu.Unlock()
	if runtime != nil {
		runtime.serviceStarted(w.executor, true)
	}
	defer func() {
		w.mu.Lock()
		w.running = false
		w.signalLocked()
		w.mu.Unlock()
		w.session.mu.Lock()
		w.session.services--
		w.session.mu.Unlock()
	}()
	blocked := map[LedgerClass]bool{}
	var lastEpoch uint64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if w.session.Snapshot().Closed {
			return ErrClosed
		}
		select {
		case <-w.executor.done:
			return ErrClosed
		default:
		}
		work, space := w.session.WorkChanged(), w.executor.SpaceChanged()
		w.mu.Lock()
		permission, epoch, pending := w.permissions, w.epoch, w.pending
		w.mu.Unlock()
		if epoch != lastEpoch {
			clear(blocked)
			lastEpoch = epoch
		}
		if pending != nil {
			pending.mu.Lock()
			finished, outcome, priorErr := pending.finished, pending.outcome, pending.err
			pending.mu.Unlock()
			if finished {
				if outcome == Uncertain {
					if priorErr != nil {
						return priorErr
					}
					return ErrUnknown
				}
				w.mu.Lock()
				w.pending = nil
				w.stats.ConfirmedStored++
				w.signalLocked()
				w.mu.Unlock()
				continue
			}
		} else {
			for n := 0; n < len(w.sources); n++ {
				index := (w.cursor + n) % len(w.sources)
				source := w.sources[index]
				if blocked[source.class] || !w.session.ReadyFor(w.plane, source.class) {
					continue
				}
				start := time.Now()
				p, err := source.prepare(ctx)
				w.mu.Lock()
				w.stats.PrepareTime += time.Since(start)
				w.signalLocked()
				w.mu.Unlock()
				if permissionUnavailable(err) {
					blocked[source.class] = true
					continue
				}
				if err != nil {
					return err
				}
				if p == nil {
					continue
				}
				pending = p
				w.cursor = (index + 1) % len(w.sources)
				w.mu.Lock()
				w.pending = p
				w.stats.Prepared++
				for k, n := range p.recordKindCounts() {
					w.stats.PreparedKinds[k] += n
				}
				w.signalLocked()
				w.mu.Unlock()
				break
			}
		}
		w.mu.Lock()
		if w.stats.DataBlocked != blocked[DataLedger] || w.stats.ControlBlocked != blocked[ControlLedger] {
			w.stats.DataBlocked = blocked[DataLedger]
			w.stats.ControlBlocked = blocked[ControlLedger]
			w.signalLocked()
		}
		w.mu.Unlock()
		if pending == nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-w.executor.done:
				return ErrClosed
			case <-work:
			case <-permission:
			}
			continue
		}
		start := time.Now()
		outcome, err := pending.Start(ctx, w.executor)
		w.mu.Lock()
		w.stats.PublishTime += time.Since(start)
		if outcome == Accepted {
			w.stats.Accepted++
			for k, n := range pending.recordKindCounts() {
				w.stats.AcceptedKinds[k] += n
				if n > 0 {
					w.stats.LastAcceptedKindsAtMS[k] = time.Now().UnixMilli()
				}
			}
			w.stats.AcceptedEncodedBytes += pending.usage.Attempt.EncodedBytes
			w.pending = nil
		}
		if outcome == Uncertain {
			w.stats.Uncertain++
		}
		w.signalLocked()
		w.mu.Unlock()
		if outcome == NotStarted && errors.Is(err, errCommandCapacity) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-w.executor.done:
				return ErrClosed
			case <-space:
			case <-permission:
			}
			continue
		}
		if err != nil {
			return err
		}
		if outcome != Accepted {
			return ErrUnknown
		}
	}
}
