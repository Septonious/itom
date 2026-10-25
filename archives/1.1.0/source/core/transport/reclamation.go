package transport

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"itom.local/core/journal"
)

var ErrUIDPlusRequired = errors.New("V2_IMAP_UIDPLUS_REQUIRED")

// ReclamationStats describes original physical debt only. A logical receipt,
// STORE completion or successful EXPUNGE never increments Released by itself.
type ReclamationStats struct {
	Queued, Released, Refused, Probes uint64
	ReceiptProbes, PeerReceipts       uint64
	Batches, MaxBatch                 uint64
	Pending                           int
	Disabled                          bool
}

// Only RemoteReceiver can mint this candidate: its original journal has
// registered the exact descriptor and observed the complete authenticated BODY.
// Inbox queues a peer candidate only AFTER all records apply/discard. Own
// SERVER bodies require their original observed row AND a per-body client MAC
// receipt before deletion. Startup messages never enter this cleanup path.
type reclaimBody struct {
	store           *journal.Store
	ref             journal.AttemptRef
	location        journal.Location
	account, folder string
	absenceOnly     bool
	receiptKeyword  string
	receiptVerified bool
}

// EnableReclamation opts an issuer's scoped inbox into peer-body reclamation.
// Call before creating its worker. The same selected executor is used; this
// creates no connection, timer, grant, refund shortcut or generic EXPUNGE.
// Providers without exact UID EXPUNGE support must retain debt instead.
func (i *Inbox) EnableReclamation() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if !i.server || i.session == nil {
		return ErrProtocol
	}
	i.session.mu.Lock()
	defer i.session.mu.Unlock()
	if i.session.services != 0 || i.session.runtime != nil {
		return ErrProtocol
	}
	if !i.executor.uidPlus {
		return ErrUIDPlusRequired
	}
	if i.reclaimQueue == nil {
		i.reclaimQueue = make(map[uint32]*reclaimBody)
	}
	i.ensureMaintenanceWake()
	i.reclamation = true
	return nil
}

func (i *Inbox) ReclamationStats() ReclamationStats {
	i.mu.Lock()
	defer i.mu.Unlock()
	s := i.reclaimStats
	s.Pending = len(i.reclaimQueue)
	return s
}

func (i *Inbox) queueReclamation(uid uint32, delivery InboxDelivery) {
	if tagged, ok := delivery.(*ledgerDelivery); ok {
		delivery = tagged.InboxDelivery
	}
	if d, ok := delivery.(*ClientDelivery); ok && i.receiptEnabled && d.receipt != nil {
		if i.receipts[uid] == nil && len(i.receipts) < i.limits.KnownUIDs {
			i.receipts[uid] = d.receipt
			i.receiptStats.Queued++
			i.signalMaintenance()
		}
		return
	}
	d, ok := delivery.(*StoredDelivery)
	if !i.reclamation || !ok || d.reclaim == nil {
		return
	}
	// The enclosing inbox turn owns the delivery; Apply already finished. The
	// queue is bounded by the existing actual-UID inventory, not a new allowance.
	i.queueReclaimToken(uid, d.reclaim)
}

func (i *Inbox) queueReclaimToken(uid uint32, r *reclaimBody) {
	if !i.reclamation {
		return
	}
	if r.account != i.scope.Account || r.folder != i.scope.Folder || r.location.UIDValidity != i.scope.UIDValidity || r.location.UID != uid || len(i.reclaimQueue) >= i.limits.KnownUIDs {
		return // no deletion/refund authority on an inconsistent/bounded route
	}
	if i.reclaimQueue[uid] == nil {
		i.reclaimQueue[uid] = r
		i.reclaimStats.Queued++
		i.signalMaintenance()
	}
}

// A detached command belongs to this exact batch across cancellation. Scope,
// original issuer and post-expunge state cannot change while a tag drains.
type reclamationBatch struct {
	bodies  []*reclaimBody
	stage   uint8 // flags, STORE, EXPUNGE, exact-set absence
	command *Command
}

func bodyUIDs(bodies []*reclaimBody, awaitingReceipt bool) []uint32 {
	var uids []uint32
	for _, r := range bodies {
		if !awaitingReceipt || r.receiptKeyword != "" && !r.receiptVerified {
			uids = append(uids, r.location.UID)
		}
	}
	return uids
}

// reclaimOne drains at most one already-ready bounded group, without a fill
// timer. It never combines issuers/ledgers or re-expunges a still-visible UID.
func (i *Inbox) reclaimOne(ctx context.Context) (bool, error) {
	i.maintenanceMu.Lock()
	defer i.maintenanceMu.Unlock()
	i.mu.Lock()
	if !i.reclamation || len(i.reclaimQueue) == 0 {
		i.mu.Unlock()
		return false, nil
	}
	batch := i.reclaimBatch
	if batch == nil {
		var ordered []uint32
		for uid := range i.reclaimQueue {
			ordered = append(ordered, uid)
		}
		sort.Slice(ordered, func(a, b int) bool {
			left, right := ordered[a] > i.reclaimCursor, ordered[b] > i.reclaimCursor
			if left != right {
				return left
			}
			return ordered[a] < ordered[b]
		})
		candidates := make([]*reclaimBody, 0, len(ordered))
		for _, uid := range ordered {
			candidates = append(candidates, i.reclaimQueue[uid])
		}
		i.mu.Unlock()
		batch = &reclamationBatch{}
		for _, r := range candidates {
			if r.receiptKeyword != "" && r.store.LocalPublicationActive(r.ref) {
				continue
			}
			if len(batch.bodies) > 0 && (r.store != batch.bodies[0].store || r.absenceOnly != batch.bodies[0].absenceOnly) {
				continue
			}
			batch.bodies = append(batch.bodies, r)
			if len(batch.bodies) == journal.MaxProofBatch {
				break
			}
		}
		if len(batch.bodies) == 0 {
			return false, nil
		}
		if batch.bodies[0].absenceOnly {
			batch.stage = 3
		}
		i.mu.Lock()
		i.reclaimCursor = batch.bodies[len(batch.bodies)-1].location.UID
		i.reclaimBatch = batch
	}
	i.mu.Unlock()
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if batch.stage == 0 && len(bodyUIDs(batch.bodies, true)) == 0 {
			batch.stage = 1
		}
		if batch.command == nil {
			var err error
			switch batch.stage {
			case 0:
				batch.command, err = i.executor.fetchUIDSet(ctx, bodyUIDs(batch.bodies, true), true)
			case 1:
				batch.command, err = i.executor.deleteUIDSet(ctx, bodyUIDs(batch.bodies, false), false)
			case 2:
				batch.command, err = i.executor.deleteUIDSet(ctx, bodyUIDs(batch.bodies, false), true)
			case 3:
				batch.command, err = i.executor.fetchUIDSet(ctx, bodyUIDs(batch.bodies, false), false)
			default:
				return false, ErrProtocol
			}
			if err != nil {
				return false, err
			}
		}
		bodies, err := batch.command.Wait(ctx)
		if err != nil {
			if errors.Is(err, ErrRefused) {
				i.mu.Lock()
				i.reclamation = false
				i.reclaimStats.Refused++
				i.reclaimStats.Disabled = true
				i.mu.Unlock()
				return false, nil
			}
			return false, err
		}
		batch.command = nil
		switch batch.stage {
		case 0:
			flags := make(map[uint32][]string, len(bodies))
			for _, body := range bodies {
				flags[body.UID] = body.Flags
			}
			ready := batch.bodies[:0]
			var newlyVerified uint64
			for _, r := range batch.bodies {
				if r.receiptKeyword != "" && !r.receiptVerified {
					for _, flag := range flags[r.location.UID] {
						if flag == r.receiptKeyword {
							r.receiptVerified = true
							newlyVerified++
							break
						}
					}
				}
				if r.receiptKeyword == "" || r.receiptVerified {
					ready = append(ready, r)
				}
			}
			batch.bodies = ready
			i.mu.Lock()
			i.reclaimStats.ReceiptProbes++
			i.reclaimStats.PeerReceipts += newlyVerified
			if len(ready) == 0 {
				i.reclaimBatch = nil
				i.mu.Unlock()
				return false, nil
			}
			i.mu.Unlock()
			batch.stage = 1
		case 1:
			batch.stage = 2
		case 2:
			for _, r := range batch.bodies {
				r.absenceOnly = true
			}
			i.mu.Lock()
			i.reclaimStats.Batches++
			i.reclaimStats.MaxBatch = max(i.reclaimStats.MaxBatch, uint64(len(batch.bodies)))
			i.mu.Unlock()
			batch.stage = 3
		case 3:
			present := make(map[uint32]bool, len(bodies))
			for _, body := range bodies {
				present[body.UID] = true
			}
			var proofs []journal.Deletion
			for _, r := range batch.bodies {
				if !present[r.location.UID] {
					proofs = append(proofs, journal.Deletion{Ref: r.ref, Location: r.location})
				}
			}
			i.mu.Lock()
			i.reclaimStats.Probes++
			i.mu.Unlock()
			if len(proofs) > 0 {
				store := batch.bodies[0].store
				if err := store.DeletedMany(store.Run(), proofs); err != nil {
					return false, err
				}
				i.session.notifyReclaimed(i.scope.Account, store.StoreID())
			}
			i.mu.Lock()
			for _, proof := range proofs {
				delete(i.reclaimQueue, proof.Location.UID)
			}
			i.reclaimStats.Released += uint64(len(proofs))
			i.reclaimBatch = nil
			i.mu.Unlock()
			return len(proofs) > 0, nil
		}
	}
}

// deleteUIDSet is private: only the scoped post-delivery reclamation owner
// reaches it. UIDPLUS prevents unrelated messages flagged by another session
// from being expunged. Physical absence must still be verified separately.
func (e *Executor) deleteUIDSet(ctx context.Context, uids []uint32, expunge bool) (*Command, error) {
	_, set, err := checkedUIDSet(uids)
	if err != nil {
		return nil, err
	}
	if !e.uidPlus {
		return nil, ErrUIDPlusRequired
	}
	if e.account == "" || e.mailbox == "" || e.validity == 0 {
		return nil, ErrProtocol
	}
	if err := e.lockWriter(ctx); err != nil {
		return nil, err
	}
	defer func() { <-e.writer }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	class, command := commandStore, fmt.Sprintf("UID STORE %s +FLAGS.SILENT (\\Deleted)", set)
	if expunge {
		class, command = commandExpunge, fmt.Sprintf("UID EXPUNGE %s", set)
	}
	tag, op, err := e.begin(class, false, false)
	if err != nil {
		return nil, err
	}
	if err := e.write([]byte(tag + " " + command + "\r\n")); err != nil {
		e.fail(ErrUnknown)
		return &Command{op}, ErrUnknown
	}
	return &Command{op}, nil
}
