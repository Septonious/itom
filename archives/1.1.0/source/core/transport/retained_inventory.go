package transport

import (
	"context"
	"crypto/sha256"
	"errors"
	"itom.local/core/journal"
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

func inspectRetained(ctx context.Context, x *Executor, user string, key []byte, stores []*journal.Store) (*RetainedInventory, error) {
	if x == nil || len(key) != 32 || user == "" {
		return nil, ErrProtocol
	}
	snapshot := &RetainedInventory{account: x.account, folder: x.mailbox, user: user, validity: x.validity, keyHash: sha256.Sum256(key), sessions: map[uint32]wire.ID{}}
	candidates := map[uint32]*reclaimBody{}
	err := visitBodies(ctx, x, func(body Body) (bool, error) {
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
		if e.class != 0 && x.uidPlus && x.keywords {
			for _, store := range stores {
				if store == nil || u.Key.Store != store.StoreID() {
					continue
				}
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
				observation := journal.Observation{Account: x.account, Folder: x.mailbox, EncodedBytes: e.charge, Location: journal.Location{UIDValidity: x.validity, UID: body.UID}, Decoded: e.body}
				if err = store.Observe(ref, store.Run(), observation); err != nil {
					return false, err
				}
				candidates[body.UID] = &reclaimBody{store: store, ref: ref, location: observation.Location, account: x.account, folder: x.mailbox, receiptKeyword: receiptKeyword(e.body, x.validity, body.UID, key)}
			}
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	if err = reclaimReceivedInventory(ctx, x, candidates, snapshot); err != nil {
		return nil, err
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
func reclaimReceivedInventory(ctx context.Context, x *Executor, candidates map[uint32]*reclaimBody, snapshot *RetainedInventory) error {
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
