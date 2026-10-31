package transport

import (
	"errors"
	"itom.local/core/journal"
	"itom.local/core/secure"
)

// acceptBody is shared by complete inventory and predicted UID reads.
// Actual UID/scope, physical accounting and replay semantics are identical.
func (i *Inbox) acceptBody(body Body, available inboxAdmission) (InboxProgress, bool, error) {
	var progress InboxProgress
	var delivery InboxDelivery
	var err error
	uid := body.UID
	if i.admit != nil {
		delivery, err = i.admit(i.scope.UIDValidity, body, available)
	} else {
		delivery, err = i.decode(i.scope.UIDValidity, body)
	}
	if errors.Is(err, journal.ErrOrder) || errors.Is(err, ErrCapacity) {
		if errors.Is(err, journal.ErrOrder) {
			progress.OrderWaits++
			i.forward = true
		}
		var blocked *inboxDeferred
		if errors.As(err, &blocked) && !blocked.transient {
			i.deferred[uid] = blocked.class
			if int(blocked.kind) < len(progress.DeferredKinds) {
				progress.DeferredKinds[blocked.kind]++
			}
			if blocked.background {
				progress.RecoveryWaits++
				if i.recoveryDeferred == nil {
					i.recoveryDeferred = map[uint32]bool{}
				}
				i.recoveryDeferred[uid] = true
			}
		}
		progress.Deferred++
		return progress, false, nil
	}
	if errors.Is(err, secure.ErrReplay) {
		i.seen[uid] = true
		return progress, true, nil
	}
	if err != nil {
		return progress, false, err
	}
	delete(i.deferred, uid)
	delete(i.recoveryDeferred, uid)
	if delivery != nil {
		progressOnly, background := false, false
		if tagged, ok := delivery.(*ledgerDelivery); ok {
			progressOnly = tagged.progress
			background = tagged.background
		}
		if progressOnly && (!available.progress || deliveryClass(delivery) != ControlLedger) || !progressOnly && (background && !available.recovery || !background && !available.allows(deliveryClass(delivery))) {
			return progress, false, ErrProtocol
		}
		if err = delivery.Apply(); errors.Is(err, ErrCapacity) {
			if progressOnly {
				return progress, false, ErrProtocol
			}
			i.pending[uid] = delivery
			progress.Deferred++
			return progress, false, nil
		} else if err != nil {
			return progress, false, err
		}
	}
	i.seen[uid] = true
	i.queueReclamation(uid, delivery)
	progress.Applied++
	return progress, true, nil
}
