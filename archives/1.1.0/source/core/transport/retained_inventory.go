package transport

import (
	"context"
	"crypto/sha256"
	"errors"
	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"sort"

	"itom.local/core/wire"
)

// RetainedInventory is sealed discovery knowledge from full authenticated BODY
// reads. It is never delivery, financial registration, deletion or absence
// proof. UIDVALIDITY and the authentication identity pin every reusable hint.
type RetainedInventory struct {
	account, folder, user string
	validity              uint32
	keyHash               [32]byte
	sessions              map[uint32]wire.ID
}

// InspectRetained validates the original bounded inventory, preserving a small
// hint for each actual BODY. It neither closes old grants nor refunds old mail.
func InspectRetained(ctx context.Context, x *Executor, user string, key []byte) (*RetainedInventory, error) {
	return inspectRetained(ctx, x, user, key, nil)
}

// InspectRetainedAndReclaimReceived keeps ordinary discovery hints separate
// from financial proof. Only our original local SERVER attempts with full BODY
// observation AND the peer's per-body authenticated delivery receipt can enter
// the existing exact UID EXPUNGE/independent absence machinery. No old CLIENT
// body, missing journal row, unreceived mail or old namespace is reclaimed.
func InspectRetainedAndReclaimReceived(ctx context.Context, x *Executor, user string, key []byte, stores ...*journal.Store) (*RetainedInventory, error) {
	return inspectRetained(ctx, x, user, key, stores)
}

func inspectRetained(ctx context.Context, x *Executor, user string, key []byte, stores []*journal.Store) (result *RetainedInventory, resultErr error) {
	if x == nil || len(key) != 32 || user == "" {
		return nil, ErrProtocol
	}
	x.mu.Lock()
	x.retainedStats = RetainedInventoryStats{}
	x.mu.Unlock()
	stage := func(value string) { x.mu.Lock(); x.retainedStats.Stage = value; x.mu.Unlock() }
	defer func() {
		x.mu.Lock()
		defer x.mu.Unlock()
		if resultErr != nil {
			x.retainedStats.Failure = retainedFailureCode(resultErr)
		} else {
			x.retainedStats.Stage = "complete"
		}
	}()
	snapshot := &RetainedInventory{account: x.account, folder: x.mailbox, user: user, validity: x.validity, keyHash: sha256.Sum256(key), sessions: map[uint32]wire.ID{}}
	candidates := map[uint32]*reclaimBody{}
	references := map[journal.AttemptRef]uint32{}
	ambiguous := map[journal.AttemptRef]bool{}
	conflict := func(ref journal.AttemptRef, err error) {
		ambiguous[ref] = true
		delete(candidates, references[ref])
		x.mu.Lock()
		x.retainedStats.ProofConflicts++
		x.retainedStats.DeferredFailure = retainedFailureCode(err)
		x.retainedStats.DeferredStage = x.retainedStats.Stage
		x.mu.Unlock()
	}
	err := visitBodiesWithStage(ctx, x, func(body Body) (bool, error) {
		e, err := openMailboxEnvelope(body.Bytes, key)
		if err != nil {
			return false, err
		}
		defer clear(e.body)
		u := e.usage
		if body.UID == 0 || u.Attempt == nil || u.Key.Account != x.account || u.Key.User != user || u.Attempt.Folder != x.mailbox {
			return false, ErrProtocol
		}
		if len(snapshot.sessions) >= DefaultInboxLimits().KnownUIDs {
			return false, ErrCapacity
		}
		snapshot.sessions[body.UID] = u.Key.Session
		x.mu.Lock()
		x.retainedStats.ObservedBodies++
		x.mu.Unlock()
		if e.class != 0 && x.uidPlus && x.keywords {
			for _, store := range stores {
				if store == nil || u.Key.Store != store.StoreID() {
					continue
				}
				stage("journal_correlate")
				ref, err := store.LocalAttempt(u.Key, u.Attempt.Batch)
				if errors.Is(err, journal.ErrMissing) || errors.Is(err, journal.ErrScope) {
					continue
				}
				if err != nil {
					return false, err
				}
				if store.LocalPublicationActive(ref) {
					continue
				}
				if uid, ok := references[ref]; ok && uid != body.UID {
					conflict(ref, journal.ErrState)
					continue
				}
				references[ref] = body.UID
				if ambiguous[ref] {
					continue
				}
				observation := journal.Observation{Account: x.account, Folder: x.mailbox, EncodedBytes: e.charge, Location: journal.Location{UIDValidity: x.validity, UID: body.UID}, Decoded: e.body}
				stage("journal_observe")
				if err = store.Observe(ref, store.Run(), observation); err != nil {
					if retainedProofConflict(err) {
						conflict(ref, err)
						continue
					}
					return false, err
				}
				candidates[body.UID] = &reclaimBody{store: store, ref: ref, location: observation.Location, account: x.account, folder: x.mailbox, receiptKeyword: receiptKeyword(e.body, x.validity, body.UID, key)}
			}
		}
		return false, nil
	}, stage)
	if err != nil {
		return nil, err
	}
	stage("cleanup_receipts")
	x.mu.Lock()
	x.retainedStats.CleanupCandidates = uint64(len(candidates))
	x.mu.Unlock()
	deferred := false
	if err = reclaimReceivedInventory(ctx, x, candidates, snapshot, stage); err != nil {
		// The full authenticated inventory already completed. A failed optional
		// proof cannot authorize a refund or block the planned service socket.
		// Cancellation, poisoned/corrupt journals and unclassified errors stay fatal.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !deferRetainedCleanup(err) {
			return nil, err
		}
		deferred = true
		x.mu.Lock()
		x.retainedStats.CleanupDeferred = true
		x.retainedStats.DeferredFailure = retainedFailureCode(err)
		x.retainedStats.DeferredStage = x.retainedStats.Stage
		x.mu.Unlock()
	}
	if deferred && x.Snapshot().Closed {
		return snapshot, nil
	}
	if err = x.AdoptRetainedInventory(snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
}

// AdoptRetainedInventory transfers verified read knowledge across the server's
// temporary inventory connection and its selected service connection. A new
// mailbox incarnation invalidates all hints; no UID is treated as absent.
func (e *Executor) AdoptRetainedInventory(s *RetainedInventory) error {
	if s == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed != nil {
		return e.closed
	}
	if s.account != e.account || s.folder != e.mailbox || s.validity == 0 {
		return ErrProtocol
	}
	if s.validity != e.validity {
		return nil
	}
	if len(s.sessions) > DefaultInboxLimits().KnownUIDs {
		return ErrCapacity
	}
	owned := *s
	owned.sessions = make(map[uint32]wire.ID, len(s.sessions))
	for uid, session := range s.sessions {
		owned.sessions[uid] = session
	}
	e.retained = &owned
	return nil
}

// Called only after full outer-envelope authentication under the profile key.
// The client has already read these bodies while discovering advertisements;
// retaining their session identities avoids reading them again at handoff.
func (e *Executor) rememberRetained(body Body, user string, key []byte) {
	if len(key) != 32 {
		return
	}
	env, err := openMailboxEnvelope(body.Bytes, key)
	if err != nil {
		return
	}
	defer clear(env.body)
	u := env.usage
	if body.UID == 0 || u.Attempt == nil || u.Key.Account != e.account || u.Key.User != user || u.Attempt.Folder != e.mailbox {
		return
	}
	hash := sha256.Sum256(key)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.retained == nil {
		e.retained = &RetainedInventory{account: e.account, folder: e.mailbox, user: user, validity: e.validity, keyHash: hash, sessions: map[uint32]wire.ID{}}
	}
	r := e.retained
	if r.user != user || r.keyHash != hash || r.validity != e.validity || len(r.sessions) >= DefaultInboxLimits().KnownUIDs {
		return
	}
	r.sessions[body.UID] = u.Key.Session
}

func (i *Inbox) adoptHistoricalReads() {
	e := i.executor
	e.mu.Lock()
	defer e.mu.Unlock()
	e.historicalReadHints = 0
	h, r := e.history, e.retained
	if h == nil || r == nil || r.account != i.scope.Account || r.folder != i.scope.Folder || r.validity != i.scope.UIDValidity || r.user != h.user || r.keyHash != sha256.Sum256(h.key[:]) {
		return
	}
	for uid, session := range r.sessions {
		if session != h.session && len(i.seen) < i.limits.KnownUIDs {
			i.seen[uid] = true
			e.historicalReadHints++
		}
	}
}

// This pre-session owner reuses precisely the runtime's receipt/delete/proof
// implementation. It has no logical Session, authority queue or grant issuer.
func reclaimReceivedInventory(ctx context.Context, x *Executor, candidates map[uint32]*reclaimBody, snapshot *RetainedInventory, stage func(string)) error {
	if len(candidates) == 0 {
		return nil
	}
	var uids []uint32
	for uid := range candidates {
		uids = append(uids, uid)
	}
	sort.Slice(uids, func(a, b int) bool { return uids[a] < uids[b] })
	inbox := &Inbox{executor: x, scope: InboxScope{Account: x.account, Folder: x.mailbox, UIDValidity: x.validity}, limits: DefaultInboxLimits(), reclamation: true, reclaimQueue: map[uint32]*reclaimBody{}}
	inbox.ensureMaintenanceWake()
	for start := 0; start < len(uids); start += journal.MaxProofBatch {
		stage("cleanup_flags")
		command, err := x.fetchMaintenance(ctx, uids[start:min(start+journal.MaxProofBatch, len(uids))], true)
		if err != nil {
			return err
		}
		flags, err := command.Wait(ctx)
		if err != nil {
			return err
		}
		for _, body := range flags {
			candidate := candidates[body.UID]
			if candidate != nil && verifiedReceiptKeyword(body.Flags, candidate.receiptKeyword) {
				candidate.receiptVerified = true
				inbox.queueReclaimToken(body.UID, candidate)
			}
		}
	}
	for len(inbox.reclaimQueue) > 0 {
		stage("cleanup_delete_and_absence")
		progress, err := inbox.reclaimOne(ctx)
		if err != nil {
			return err
		}
		if !progress {
			break
		}
	}
	for uid := range candidates {
		// Only candidates that had a verified receipt AND completed the original
		// independent absence proof can be removed from discovery hints.
		if candidates[uid].receiptVerified && inbox.reclaimQueue[uid] == nil {
			delete(snapshot.sessions, uid)
		}
	}
	return nil
}

// Fixed labels/counters only. No provider text, UID, destination, BODY or key.
type RetainedInventoryStats struct {
	DeferredStage     string `json:"deferred_stage,omitempty"`
	Stage             string `json:"stage,omitempty"`
	Failure           string `json:"failure,omitempty"`
	DeferredFailure   string `json:"deferred_failure,omitempty"`
	ObservedBodies    uint64 `json:"observed_bodies"`
	CleanupCandidates uint64 `json:"cleanup_candidates"`
	ProofConflicts    uint64 `json:"proof_conflicts"`
	CleanupDeferred   bool   `json:"cleanup_deferred"`
}

func retainedFailureCode(err error) string {
	for _, known := range []error{context.Canceled, context.DeadlineExceeded, mailbox.ErrScope, mailbox.ErrLimit, wire.ErrFormat, wire.ErrLimit, wire.ErrVersion, wire.ErrDataEnvelopeDigest, ErrRefused} {
		if errors.Is(err, known) {
			return known.Error()
		}
	}
	return serviceFailure(err)
}
func retainedProofConflict(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, journal.ErrPoisoned) || errors.Is(err, journal.ErrCorrupt) || errors.Is(err, journal.ErrGeneration) {
		return false
	}
	return errors.Is(err, journal.ErrState) || errors.Is(err, journal.ErrScope) || errors.Is(err, journal.ErrMissing)
}

func deferRetainedCleanup(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, journal.ErrPoisoned) || errors.Is(err, journal.ErrCorrupt) || errors.Is(err, journal.ErrGeneration) {
		return false
	}
	for _, known := range []error{ErrRefused, ErrUnknown, ErrClosed, ErrProtocol, ErrCapacity, journal.ErrState, journal.ErrScope, journal.ErrMissing} {
		if errors.Is(err, known) {
			return true
		}
	}
	return false
}
