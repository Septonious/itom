package transport

import (
	"context"
	"errors"
	"sort"
	"sync"

	"itom.local/core/wire"
)

// InboxDelivery owns plaintext authenticated once and, where required, durably
// accounted by the scoped decoder. Apply may wait for logical receiver space.
type InboxDelivery interface{ Apply() error }
type InboxAction func() error

func (f InboxAction) Apply() error { return f() }

type InboxDecoder func(validity uint32, body Body) (InboxDelivery, error)

type inboxAdmission struct{ data, control, progress bool }

func (a inboxAdmission) allows(class LedgerClass) bool {
	return class == DataLedger && a.data || class == ControlLedger && a.control
}

type scopedInboxDecoder func(uint32, Body, inboxAdmission) (InboxDelivery, error)
type ledgerDelivery struct {
	InboxDelivery
	class    LedgerClass
	progress bool
}

func deliveryClass(d InboxDelivery) LedgerClass {
	if v, ok := d.(*ledgerDelivery); ok {
		return v.class
	}
	return DataLedger
}
func classifyDelivery(d InboxDelivery, class LedgerClass, progress bool, err error) (InboxDelivery, error) {
	if d == nil {
		return nil, err
	}
	return &ledgerDelivery{InboxDelivery: d, class: class, progress: progress}, err
}
func standaloneInboxDecoder(decode scopedInboxDecoder) InboxDecoder {
	return func(validity uint32, body Body) (InboxDelivery, error) {
		delivery, err := decode(validity, body, inboxAdmission{data: true, control: true})
		// Preserve the standalone decoder API; a generic Inbox has one shared
		// delivery bound. Only the scoped factories install ledger admission.
		if tagged, ok := delivery.(*ledgerDelivery); ok {
			return tagged.InboxDelivery, err
		}
		return delivery, err
	}
}

type inboxDeferred struct {
	class     LedgerClass
	transient bool
}

func (e *inboxDeferred) Error() string { return ErrCapacity.Error() }
func (e *inboxDeferred) Unwrap() error { return ErrCapacity }

// Limits speculative BODY work while one ledger is blocked. The future carrier
// coordinator must honor More and round-robin other ready service, not hot-poll.
const maxBlockedInboxProbes = 8

type InboxLimits struct {
	// PendingDeliveries bounds decoded DATA; PendingControls reserves a separate
	// bounded protocol queue in the scoped factories. Neither is quota authority.
	KnownUIDs, PendingDeliveries, PendingControls int
}

func DefaultInboxLimits() InboxLimits {
	return InboxLimits{KnownUIDs: 8192, PendingDeliveries: 2, PendingControls: 2}
}

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
	mu                  sync.Mutex
	session             *Session
	routes              map[LedgerClass]wire.ID
	executor            *Executor
	scope               InboxScope
	limits              InboxLimits
	decode              InboxDecoder
	admit               scopedInboxDecoder
	seen                map[uint32]bool
	bootstrapUIDs       map[uint32]bool
	pending             map[uint32]InboxDelivery
	deferred            map[uint32]LedgerClass
	examined            map[uint32]bool
	continuing, forward bool
	tailReady           bool
	tailUID             uint32
	tailSequence        uint32
	tailVersion         uint64
}
type InboxProgress struct {
	Observed, Applied, Deferred int
	More                        bool
}

func NewInbox(executor *Executor, scope InboxScope, limits InboxLimits, decode InboxDecoder) (*Inbox, error) {
	if executor == nil || scope.Account == "" || scope.Account != executor.account || scope.Folder != executor.mailbox || scope.UIDValidity == 0 || scope.UIDValidity != executor.validity || limits.KnownUIDs < 1 || limits.KnownUIDs > 65536 || limits.PendingDeliveries < 1 || limits.PendingDeliveries > 32 || limits.PendingControls < 1 || limits.PendingControls > 32 || decode == nil {
		return nil, ErrProtocol
	}
	return &Inbox{executor: executor, scope: scope, limits: limits, decode: decode, seen: make(map[uint32]bool), pending: make(map[uint32]InboxDelivery), deferred: make(map[uint32]LedgerClass), examined: make(map[uint32]bool)}, nil
}

func newScopedInbox(executor *Executor, session *Session, scope InboxScope, limits InboxLimits, decode scopedInboxDecoder) (*Inbox, error) {
	if decode == nil || session == nil {
		return nil, ErrProtocol
	}
	i, err := NewInbox(executor, scope, limits, standaloneInboxDecoder(decode))
	if err != nil {
		return nil, err
	}
	i.admit = decode
	i.session = session
	return i, nil
}

func (i *Inbox) admission() inboxAdmission {
	data, control := 0, 0
	for _, d := range i.pending {
		if deliveryClass(d) == ControlLedger {
			control++
		} else {
			data++
		}
	}
	return inboxAdmission{data: data < i.limits.PendingDeliveries, control: i.admit != nil && control < i.limits.PendingControls, progress: i.admit != nil}
}

func (i *Inbox) Sweep(ctx context.Context) (InboxProgress, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.sweepLocked(ctx)
}

func (i *Inbox) sweepLocked(ctx context.Context) (InboxProgress, error) {
	i.tailReady = false
	version := i.executor.SequenceVersion()
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
	var tail, tailUID uint32
	for _, body := range metadata {
		tail = max(tail, body.Sequence)
		tailUID = max(tailUID, body.UID)
		uids = append(uids, body.UID)
		present[body.UID] = true
	}
	sort.Slice(uids, func(a, b int) bool { return uids[a] < uids[b] })
	progress.Observed = len(uids)
	for uid := range i.seen {
		if !present[uid] && !i.bootstrapUIDs[uid] {
			delete(i.seen, uid)
		}
	} // discovery state only; NEVER a deletion/refund proof
	retained := 0
	for uid := range i.bootstrapUIDs {
		if !present[uid] {
			retained++
		}
	}
	if len(metadata)+retained > i.limits.KnownUIDs {
		return progress, ErrCapacity
	}
	for uid := range i.deferred {
		if !present[uid] {
			delete(i.deferred, uid)
		}
	}
	if !i.continuing {
		clear(i.examined)
		i.forward = false
	}
	i.continuing = false
	for uid := range i.examined {
		if !present[uid] {
			delete(i.examined, uid)
		}
	}
	// A decoded body can be consumed even if another worker already removed its
	// physical mail. It is retained here, not decrypted/replayed a second time.
	pendingUIDs := make([]uint32, 0, len(i.pending))
	for uid := range i.pending {
		pendingUIDs = append(pendingUIDs, uid)
	}
	sort.Slice(pendingUIDs, func(a, b int) bool {
		left, right := deliveryClass(i.pending[pendingUIDs[a]]), deliveryClass(i.pending[pendingUIDs[b]])
		if left != right {
			return left == ControlLedger
		}
		return pendingUIDs[a] < pendingUIDs[b]
	})
	for _, uid := range pendingUIDs {
		delivery := i.pending[uid]
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
	blockedProbes := 0
	for pass := 0; pass < 2; pass++ {
		advanced := false
		for low, high := 0, len(uids)-1; low <= high; {
			available := i.admission()
			var uid uint32
			if !available.data && available.control && !i.forward {
				uid = uids[high]
				high--
			} else {
				uid = uids[low]
				low++
			}
			if i.seen[uid] || i.pending[uid] != nil {
				continue
			}
			if i.examined[uid] {
				continue
			}
			if !available.data && !available.control && !available.progress {
				progress.Deferred++
				continue
			}
			if class, ok := i.deferred[uid]; ok && !available.allows(class) {
				progress.Deferred++
				continue
			}
			if i.admit != nil && (!available.data || !available.control) {
				if blockedProbes == maxBlockedInboxProbes {
					progress.More = true
					i.continuing = true
					return progress, nil
				}
				blockedProbes++
			}
			i.examined[uid] = true
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
			step, madeProgress, err := i.acceptBody(bodies[0], available)
			if err != nil {
				return progress, err
			}
			progress.Applied += step.Applied
			progress.Deferred += step.Deferred
			advanced = advanced || madeProgress
		}
		if !advanced {
			break
		}
		clear(i.examined)
	}
	ready := version == i.executor.SequenceVersion()
	for _, uid := range uids {
		if !i.seen[uid] {
			ready = false
			break
		}
	}
	if ready {
		i.tailReady = true
		i.tailSequence = tail
		i.tailUID = tailUID
		i.tailVersion = version
	}
	return progress, nil
}
