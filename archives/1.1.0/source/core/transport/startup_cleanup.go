package transport

import (
	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/wire"
)

// Only fully authenticated, actually observed current-startup bodies enter this
// private handoff. These are candidates, never absence or release proof. The
// ordinary receipt, exact UID EXPUNGE and independent absence checks still run.
type startupCleanup struct {
	receipts map[uint32]string
	reclaim  map[uint32]*reclaimBody
}

func (s *startupCleanup) remember(client bool, origin ServerOrigin, uid uint32, envelope mailboxEnvelope, key []byte, candidate *reclaimBody) {
	if client {
		if envelope.class == 0 {
			return
		} // our own CLIENT body is not peer delivery
		if s.receipts == nil {
			s.receipts = map[uint32]string{}
		}
		s.receipts[uid] = receiptKeyword(envelope.body, origin.UIDValidity, uid, key)
	} else if candidate != nil {
		if s.reclaim == nil {
			s.reclaim = map[uint32]*reclaimBody{}
		}
		candidate.startup = true
		s.reclaim[uid] = candidate
	}
}
func (s startupCleanup) clone() startupCleanup {
	n := startupCleanup{receipts: map[uint32]string{}, reclaim: map[uint32]*reclaimBody{}}
	for uid, keyword := range s.receipts {
		n.receipts[uid] = keyword
	}
	for uid, candidate := range s.reclaim {
		value := *candidate
		n.reclaim[uid] = &value
	}
	return n
}

// Scope and original ledger are rechecked when the completed roster transfers
// these sealed candidates to runtime. Cached historical UID hints never call it.
func (i *Inbox) adoptStartupCleanup(h *BootstrapHandoff) error {
	for uid, keyword := range h.cleanup.receipts {
		if !h.client || !i.bootstrapUIDs[uid] || len(keyword) != 71 {
			return ErrProtocol
		}
	}
	for uid, candidate := range h.cleanup.reclaim {
		if h.client || candidate == nil || !i.bootstrapUIDs[uid] || candidate.store == nil || candidate.store.StoreID() != h.origin.Store || candidate.store.Run() != h.origin.Run || candidate.ref.Grant.Store != h.origin.Store || candidate.ref.ID == 0 || !candidate.startup || candidate.ref.Grant.Scope.Account != h.origin.Account || candidate.ref.Grant.Scope.User != h.origin.User || candidate.ref.Grant.Scope.Session != h.epoch.Context().Session || candidate.account != h.origin.Account || candidate.folder != h.origin.Folder || candidate.location != (journal.Location{UIDValidity: h.origin.UIDValidity, UID: uid}) {
			return ErrProtocol
		}
	}
	i.startupCleanup = h.cleanup.clone()
	return nil
}

func (i *Inbox) startupCleanupPending() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if !i.reclamation || i.reclaimStats.Disabled {
		return false
	}
	for _, candidate := range i.reclaimQueue {
		if candidate.startup {
			return true
		}
	}
	return false
}

// Startup needs three CLIENT Noise frames and one roster acknowledgement.
// Rounding a quarter of remaining quota down below four must not turn valid
// remaining capacity into a protocol error. The issuer still atomically checks
// the ORIGINAL limit; keep six message slots for invitation/server startup.
func endpointInitialAllowance(store *journal.Store, account, user string) (pool.Amount, error) {
	allowance, err := endpointAllowance(store, account, user, wire.ID{})
	if err != nil {
		return pool.Amount{}, err
	}
	if allowance.Messages < 4 {
		limit, err := store.Limit(account, user)
		if err != nil {
			return pool.Amount{}, err
		}
		held, err := store.Held(account, user)
		if err != nil {
			return pool.Amount{}, err
		}
		if held.Messages > limit.Messages || limit.Messages-held.Messages < 10 {
			return pool.Amount{}, journal.ErrLimit
		}
		allowance.Messages = 4
	}
	return allowance, nil
}
