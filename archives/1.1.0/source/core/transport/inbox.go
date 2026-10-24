package transport

import (
	"context"
	"errors"
	"sort"
	"sync"

	"itom.local/core/journal"
	"itom.local/core/secure"
)

// InboxDelivery owns plaintext authenticated once and, where required, durably
// accounted by the scoped decoder. Apply may wait for logical receiver space.
type InboxDelivery interface{ Apply() error }
type InboxAction func() error

func (f InboxAction) Apply() error { return f() }

type InboxDecoder func(validity uint32, body Body) (InboxDelivery, error)

type InboxLimits struct {
	KnownUIDs, PendingDeliveries int
}

func DefaultInboxLimits() InboxLimits { return InboxLimits{KnownUIDs: 8192, PendingDeliveries: 2} }

type InboxScope struct {
	Account, Folder string
	UIDValidity     uint32
}

// Inbox is a serialized, bounded inventory/delivery worker, not a refund
// authority or timing controller. Notifications are hints only. Every sweep
// enumerates actual UIDs, so holes, stale UIDNEXT and late visibility cannot
// make it advance past an unseen body. FETCH is one BODY per command: a burst
// cannot overflow the unchanged response budget and force a healthy reconnect.
type Inbox struct {
	mu       sync.Mutex
	executor *Executor
	scope    InboxScope
	limits   InboxLimits
	decode   InboxDecoder
	seen     map[uint32]bool
	pending  map[uint32]InboxDelivery
}
type InboxProgress struct{ Observed, Applied, Deferred int }

func NewInbox(executor *Executor, scope InboxScope, limits InboxLimits, decode InboxDecoder) (*Inbox, error) {
	if executor == nil || scope.Account == "" || scope.Account != executor.account || scope.Folder != executor.mailbox || scope.UIDValidity == 0 || scope.UIDValidity != executor.validity || limits.KnownUIDs < 1 || limits.KnownUIDs > 65536 || limits.PendingDeliveries < 1 || limits.PendingDeliveries > 32 || decode == nil {
		return nil, ErrProtocol
	}
	return &Inbox{executor: executor, scope: scope, limits: limits, decode: decode, seen: make(map[uint32]bool), pending: make(map[uint32]InboxDelivery)}, nil
}

func (i *Inbox) Sweep(ctx context.Context) (InboxProgress, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	var progress InboxProgress
	command, err := i.executor.Enumerate(ctx)
	if err != nil {
		return progress, err
	}
	metadata, err := command.Wait(ctx)
	if err != nil {
		return progress, err
	}
	if len(metadata) > i.limits.KnownUIDs {
		return progress, ErrCapacity
	}
	uids := make([]uint32, 0, len(metadata))
	present := make(map[uint32]bool, len(metadata))
	for _, body := range metadata {
		uids = append(uids, body.UID)
		present[body.UID] = true
	}
	sort.Slice(uids, func(a, b int) bool { return uids[a] < uids[b] })
	progress.Observed = len(uids)
	for uid := range i.seen {
		if !present[uid] {
			delete(i.seen, uid)
		}
	} // discovery state only; NEVER a deletion/refund proof
	// A decoded body can be consumed even if another worker already removed its
	// physical mail. It is retained here, not decrypted/replayed a second time.
	for uid, delivery := range i.pending {
		if err := delivery.Apply(); errors.Is(err, ErrCapacity) {
			progress.Deferred++
			continue
		} else if err != nil {
			return progress, err
		}
		delete(i.pending, uid)
		i.seen[uid] = true
		progress.Applied++
	}
	// A later UID can carry the missing earlier grant ordinal. One second pass
	// retries dependency gaps after progress, without an unbounded retry loop.
	for pass := 0; pass < 2; pass++ {
		advanced := false
		for _, uid := range uids {
			if i.seen[uid] || i.pending[uid] != nil {
				continue
			}
			if len(i.pending) >= i.limits.PendingDeliveries {
				progress.Deferred++
				continue
			}
			fetch, err := i.executor.Fetch(ctx, uid, uid)
			if err != nil {
				return progress, err
			}
			bodies, err := fetch.Wait(ctx)
			if err != nil {
				return progress, err
			}
			if len(bodies) == 0 {
				continue
			} // raced with reclamation; not refund evidence
			if len(bodies) != 1 || bodies[0].UID != uid {
				return progress, ErrProtocol
			}
			delivery, err := i.decode(i.scope.UIDValidity, bodies[0])
			if errors.Is(err, journal.ErrOrder) {
				progress.Deferred++
				continue
			}
			if errors.Is(err, secure.ErrReplay) {
				i.seen[uid] = true
				advanced = true
				continue
			}
			if err != nil {
				return progress, err
			}
			if delivery != nil {
				if err = delivery.Apply(); errors.Is(err, ErrCapacity) {
					i.pending[uid] = delivery
					progress.Deferred++
					continue
				} else if err != nil {
					return progress, err
				}
			}
			i.seen[uid] = true
			progress.Applied++
			advanced = true
		}
		if !advanced {
			break
		}
	}
	return progress, nil
}
