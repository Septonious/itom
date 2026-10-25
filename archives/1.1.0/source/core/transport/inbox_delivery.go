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
			i.forward = true
		}
		var blocked *inboxDeferred
		if errors.As(err, &blocked) && !blocked.transient {
			i.deferred[uid] = blocked.class
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
	if delivery != nil {
		progressOnly := false
		if tagged, ok := delivery.(*ledgerDelivery); ok {
			progressOnly = tagged.progress
		}
		if progressOnly && (!available.progress || deliveryClass(delivery) != ControlLedger) || !progressOnly && !available.allows(deliveryClass(delivery)) {
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
