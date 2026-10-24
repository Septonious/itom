package transport

import "sync/atomic"

// SeedInboxBlockedForTest isolates delivery-queue pressure from the unrelated
// receiver-credit invariant. It does not simulate network service or throughput.
func SeedInboxBlockedForTest(i *Inbox, uids ...uint32) func() {
	return seedBlockedInbox(i, DataLedger, uids...)
}

func SeedInboxBlockedControlsForTest(i *Inbox, uids ...uint32) func() {
	return seedBlockedInbox(i, ControlLedger, uids...)
}

func seedBlockedInbox(i *Inbox, class LedgerClass, uids ...uint32) func() {
	var released atomic.Bool
	i.mu.Lock()
	for _, uid := range uids {
		action := InboxAction(func() error {
			if !released.Load() {
				return ErrCapacity
			}
			return nil
		})
		i.pending[uid] = &ledgerDelivery{InboxDelivery: action, class: class}
	}
	i.mu.Unlock()
	return func() { released.Store(true) }
}
