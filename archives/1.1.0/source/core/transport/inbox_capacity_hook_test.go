package transport

import "sync/atomic"

// SeedInboxBlockedForTest isolates delivery-queue pressure from the unrelated
// receiver-credit invariant. It does not simulate network service or throughput.
func SeedInboxBlockedForTest(i *Inbox, uids ...uint32) func() {
	var released atomic.Bool
	i.mu.Lock()
	for _, uid := range uids {
		i.pending[uid] = InboxAction(func() error {
			if !released.Load() {
				return ErrCapacity
			}
			return nil
		})
	}
	i.mu.Unlock()
	return func() { released.Store(true) }
}
