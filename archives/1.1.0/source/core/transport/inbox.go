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

type inboxAdmission struct{ data, control, progress, recovery bool }

func (a inboxAdmission) allows(class LedgerClass) bool {
	return class == DataLedger && a.data || class == ControlLedger && a.control
}

type scopedInboxDecoder func(uint32, Body, inboxAdmission) (InboxDelivery, error)
type ledgerDelivery struct {
	InboxDelivery
	class      LedgerClass
	progress   bool
	background bool
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
	tagged := &ledgerDelivery{InboxDelivery: d, class: class, progress: progress}
	var stored *StoredDelivery
	switch value := d.(type) {
	case *StoredDelivery:
		stored = value
	case *ClientDelivery:
		stored = value.delivery
	}
	if stored != nil {
		stored.session.mu.Lock()
		owners := stored.session.authorities
		stored.session.mu.Unlock()
		tagged.background = historicalAuthorityGroup(stored.records, owners)
		if tagged.background && allAuthorityProgress(stored.records) {
			tagged.progress = true
		}
	}
	return tagged, err
}
func standaloneInboxDecoder(decode scopedInboxDecoder) InboxDecoder {
	return func(validity uint32, body Body) (InboxDelivery, error) {
		delivery, err := decode(validity, body, inboxAdmission{data: true, control: true, recovery: true})
		// Preserve the standalone decoder API; a generic Inbox has one shared
		// delivery bound. Only the scoped factories install ledger admission.
		if tagged, ok := delivery.(*ledgerDelivery); ok {
			return tagged.InboxDelivery, err
		}
		return delivery, err
	}
}

type inboxDeferred struct {
	kind       Kind
	class      LedgerClass
	background bool
	transient  bool
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
	// BodyReadBatch bounds an immediate exact-UID read after sized inventory.
	// Zero/one preserves single-body discovery; no fill timer is introduced.
	BodyReadBatch int
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
// make it advance past an unseen body. Fast prediction reads one BODY. Sized
// inventory can group two known UIDs within the unchanged response budget;
// accounting, replay and pending-delivery admission still run per BODY.
type Inbox struct {
	startupCleanup     startupCleanup
	readQuantum        int        // runtime FAST work bound; bulk and bootstrap retain their own service bounds
	mu                 sync.Mutex // maintenance state only; never held over receive I/O
	receiveMu          sync.Mutex // serial receiver state and parser turns
	maintenanceMu      sync.Mutex
	maintenanceChanged chan struct{}
	session            *Session
	routes             map[LedgerClass]wire.ID
	executor           *Executor
	scope              InboxScope
	limits             InboxLimits
	decode             InboxDecoder
	admit              scopedInboxDecoder
	seen               map[uint32]bool
	bootstrapUIDs      map[uint32]bool
	// Startup-only stop boundary: do not scan application mail after the last
	// actual bootstrap BODY has made scoped runtime handover possible.
	stopAfterApply      func() bool
	pending             map[uint32]InboxDelivery
	deferred            map[uint32]LedgerClass
	recoveryDeferred    map[uint32]bool
	examined            map[uint32]bool
	continuing, forward bool
	tailReady           bool
	tailUID             uint32
	tailSequence        uint32
	tailVersion         uint64
	server              bool
	reclamation         bool
	reclaimQueue        map[uint32]*reclaimBody
	reclaimStats        ReclamationStats
	reclaimCursor       uint32
	reclaimBatch        *reclamationBatch
	receiptEnabled      bool
	receipts            map[uint32]*deliveryReceipt
	receiptStats        DeliveryReceiptStats
}
type InboxProgress struct {
	DeferredKinds               [12]uint64
	Observed, Applied, Deferred int
	OrderWaits, RecoveryWaits   int
	More                        bool
}

func NewInbox(executor *Executor, scope InboxScope, limits InboxLimits, decode InboxDecoder) (*Inbox, error) {
	if executor == nil || scope.Account == "" || scope.Account != executor.account || scope.Folder != executor.mailbox || scope.UIDValidity == 0 || scope.UIDValidity != executor.validity || limits.KnownUIDs < 1 || limits.KnownUIDs > 65536 || limits.PendingDeliveries < 1 || limits.PendingDeliveries > 32 || limits.PendingControls < 1 || limits.PendingControls > 32 || limits.BodyReadBatch < 0 || limits.BodyReadBatch > min(2, limits.PendingDeliveries) || decode == nil {
		return nil, ErrProtocol
	}
	i := &Inbox{executor: executor, scope: scope, limits: limits, decode: decode, seen: make(map[uint32]bool), pending: make(map[uint32]InboxDelivery), deferred: make(map[uint32]LedgerClass), examined: make(map[uint32]bool)}
	i.adoptHistoricalReads()
	return i, nil
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
	data, control, background := 0, 0, 0
	for _, d := range i.pending {
		if tagged, ok := d.(*ledgerDelivery); ok && tagged.background {
			background++
		} else if deliveryClass(d) == ControlLedger {
			control++
		} else {
			data++
		}
	}
	return inboxAdmission{data: data < i.limits.PendingDeliveries, control: i.admit != nil && control+background < i.limits.PendingControls, recovery: i.admit != nil && background < max(1, i.limits.PendingControls-1) && control+background < i.limits.PendingControls, progress: i.admit != nil}
}

func (i *Inbox) Sweep(ctx context.Context) (InboxProgress, error) {
	i.receiveMu.Lock()
	defer i.receiveMu.Unlock()
	return i.sweepLocked(ctx)
}

func (i *Inbox) sweepLocked(ctx context.Context) (InboxProgress, error) {
	i.tailReady = false
	version := i.executor.SequenceVersion()
	var progress InboxProgress
	readWork := 0
	command, err := i.executor.enumerateInbox(ctx, i.limits.BodyReadBatch > 1)
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
	present := make(map[uint32]uint32, len(metadata))
	sizes := make(map[uint32]Body, len(metadata))
	var tail uint32
	// Allocation does not rewind when a replica exposes an empty or shorter
	// live inventory. Retain the highest observed UID only as a prediction;
	// pending/deferred bodies and uncertain sequence history still force a sweep.
	tailUID := i.tailUID
	for _, body := range metadata {
		tail = max(tail, body.Sequence)
		tailUID = max(tailUID, body.UID)
		uids = append(uids, body.UID)
		present[body.UID] = body.Sequence
		sizes[body.UID] = body
	}
	// Even an inventory whose BODY disappears before FETCH observed allocation.
	// This is no delivery, deletion proof, replay advance or quota release.
	i.tailUID = tailUID
	sort.Slice(uids, func(a, b int) bool { return uids[a] < uids[b] })
	progress.Observed = len(uids)
	for uid := range i.seen {
		if present[uid] == 0 && !i.bootstrapUIDs[uid] {
			delete(i.seen, uid)
		}
	} // discovery state only; NEVER a deletion/refund proof
	retained := 0
	for uid := range i.bootstrapUIDs {
		if present[uid] == 0 {
			retained++
		}
	}
	if len(metadata)+retained > i.limits.KnownUIDs {
		return progress, ErrCapacity
	}
	for uid := range i.deferred {
		if present[uid] == 0 {
			delete(i.deferred, uid)
			delete(i.recoveryDeferred, uid)
		}
	}
	if !i.continuing {
		clear(i.examined)
		i.forward = false
	}
	i.continuing = false
	for uid := range i.examined {
		if present[uid] == 0 {
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
		i.queueReclamation(uid, delivery)
		progress.Applied++
	}
	// A later UID can carry the missing earlier grant ordinal. One second pass
	// retries dependency gaps after progress, without an unbounded retry loop.
	blockedProbes := 0
	prefetched := make(map[uint32]Body)
	for pass := 0; pass < 2; pass++ {
		advanced := false
		for low, high := 0, len(uids)-1; low <= high; {
			if i.stopAfterApply != nil && i.stopAfterApply() {
				return progress, nil
			}
			available := i.admission()
			var uid uint32
			if (i.readQuantum > 0 && readWork == 0 || i.readQuantum == 0 && (!available.data || !available.recovery)) && available.control && !i.forward {
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
			if class, ok := i.deferred[uid]; ok && (!available.allows(class) || i.recoveryDeferred[uid] && !available.recovery) {
				progress.Deferred++
				continue
			}
			if i.admit != nil && (!available.data || !available.control || !available.recovery) {
				if blockedProbes == maxBlockedInboxProbes {
					progress.More = true
					i.continuing = true
					return progress, nil
				}
				blockedProbes++
			}
			if i.readQuantum > 0 && readWork >= i.readQuantum {
				progress.More = true
				i.continuing = true
				if progress.Applied > 0 {
					clear(i.examined)
					i.forward = false
				}
				return progress, nil
			}
			i.examined[uid] = true
			body, cached := prefetched[uid]
			delete(prefetched, uid)
			if !cached {
				readWork++
				group := i.readGroup(uid, uids[low:high+1], sizes, available)
				fetch, err := i.executor.fetchBodies(ctx, group)
				if err != nil {
					return progress, err
				}
				bodies, err := fetch.Wait(ctx)
				if err != nil {
					return progress, err
				}
				for _, received := range bodies {
					prefetched[received.UID] = received
				}
				body, cached = prefetched[uid]
				delete(prefetched, uid)
				if !cached {
					continue // raced with reclamation; never a refund proof
				}
			}
			step, madeProgress, err := i.acceptBody(body, available)
			if err != nil {
				return progress, err
			}
			progress.Applied += step.Applied
			progress.Deferred += step.Deferred
			progress.OrderWaits += step.OrderWaits
			progress.RecoveryWaits += step.RecoveryWaits
			for k, n := range step.DeferredKinds {
				progress.DeferredKinds[k] += n
			}
			advanced = advanced || madeProgress
		}
		if !advanced {
			break
		}
		clear(i.examined)
	}
	ready := version == i.executor.SequenceVersion()
	for index, uid := range uids {
		if present[uid] != uint32(index+1) {
			ready = false
			break
		}
	}
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
