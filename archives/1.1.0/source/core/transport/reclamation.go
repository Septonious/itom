package transport

import (
	"context"
	"errors"
	"fmt"

	"itom.local/core/journal"
)

var ErrUIDPlusRequired = errors.New("V2_IMAP_UIDPLUS_REQUIRED")

// ReclamationStats describes original physical debt only. A logical receipt,
// STORE completion or successful EXPUNGE never increments Released by itself.
type ReclamationStats struct {
	Queued, Released, Refused, Probes uint64
	ReceiptProbes, PeerReceipts       uint64
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
	stage           uint8
	command         *Command
	receiptKeyword  string
	receiptCommand  *Command
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

// reclaimOne performs at most one candidate per receive turn. A detached Wait
// retains its SAME command handle and stage across cancellation; no ambiguous
// STORE/EXPUNGE is blindly reissued. A tagged refusal disables maintenance only,
// leaving receive service and every original charge intact for recovery.
func (i *Inbox) reclaimOne(ctx context.Context) (progress bool, resultErr error) {
	i.maintenanceMu.Lock()
	defer i.maintenanceMu.Unlock()
	i.mu.Lock()
	if !i.reclamation || len(i.reclaimQueue) == 0 {
		i.mu.Unlock()
		return false, nil
	}
	uid := i.reclaimActive
	if uid == 0 {
		var first uint32
		for next := range i.reclaimQueue {
			if first == 0 || next < first {
				first = next
			}
			if next > i.reclaimCursor && (uid == 0 || next < uid) {
				uid = next
			}
		}
		if uid == 0 {
			uid = first
		}
	}
	i.reclaimActive = uid
	r := i.reclaimQueue[uid]
	i.mu.Unlock()
	defer func() {
		i.mu.Lock()
		defer i.mu.Unlock()
		if resultErr == nil {
			i.reclaimActive = 0
			i.reclaimCursor = uid
		}
	}()
	if r.receiptKeyword != "" && r.store.LocalPublicationActive(r.ref) {
		return false, nil // BODY/peer visibility may precede the local APPEND tag
	}
	if r.receiptKeyword != "" && !r.receiptVerified {
		if r.receiptCommand == nil {
			var err error
			r.receiptCommand, err = i.executor.FetchFlags(ctx, uid)
			if err != nil {
				return false, err
			}
		}
		bodies, err := r.receiptCommand.Wait(ctx)
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
		r.receiptCommand = nil
		i.mu.Lock()
		i.reclaimStats.ReceiptProbes++
		i.mu.Unlock()
		for _, body := range bodies {
			for _, flag := range body.Flags {
				if flag == r.receiptKeyword {
					r.receiptVerified = true
				}
			}
		}
		if !r.receiptVerified {
			return false, nil
		}
		i.mu.Lock()
		i.reclaimStats.PeerReceipts++
		i.mu.Unlock()
	}
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if r.command == nil {
			var err error
			switch r.stage {
			case 0:
				r.command, err = i.executor.deleteCommand(ctx, uid, false)
			case 1:
				r.command, err = i.executor.deleteCommand(ctx, uid, true)
			case 2:
				r.command, err = i.executor.fetchRange(ctx, uid, uid, true)
			default:
				return false, ErrProtocol
			}
			if err != nil {
				return false, err
			}
		}
		bodies, err := r.command.Wait(ctx)
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
		r.command = nil
		if r.stage < 2 {
			r.stage++
			continue
		}
		i.mu.Lock()
		i.reclaimStats.Probes++
		i.mu.Unlock()
		if len(bodies) != 0 {
			// A still-visible UID is NOT absence proof, even after tagged OK.
			// Probe again on a later receive turn; never refund or re-expunge.
			return false, nil
		}
		if err := r.store.Deleted(r.ref, r.store.Run(), r.location); err != nil {
			return false, err // fsync/uncertain commit fences original authority
		}
		i.mu.Lock()
		delete(i.reclaimQueue, uid)
		i.reclaimStats.Released++
		i.mu.Unlock()
		return true, nil
	}
}

// deleteCommand is private: only the scoped post-delivery reclamation owner
// reaches it. UIDPLUS prevents unrelated messages flagged by another session
// from being expunged. Physical absence must still be verified separately.
func (e *Executor) deleteCommand(ctx context.Context, uid uint32, expunge bool) (*Command, error) {
	if !e.uidPlus {
		return nil, ErrUIDPlusRequired
	}
	if e.account == "" || e.mailbox == "" || e.validity == 0 || uid == 0 {
		return nil, ErrProtocol
	}
	if err := e.lockWriter(ctx); err != nil {
		return nil, err
	}
	defer func() { <-e.writer }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	class, command := commandStore, fmt.Sprintf("UID STORE %d +FLAGS.SILENT (\\Deleted)", uid)
	if expunge {
		class, command = commandExpunge, fmt.Sprintf("UID EXPUNGE %d", uid)
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
