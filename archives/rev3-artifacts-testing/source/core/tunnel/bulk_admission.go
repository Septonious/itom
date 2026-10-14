package tunnel

import "itom.local/core/datapath"

func (l *lane) bulkAdmissionLimit() uint64 {
	if l.stripe == nil {
		return runtimeLogicalBytes
	}
	l.stripe.mu.Lock()
	defer l.stripe.mu.Unlock()
	// Only actual admitted flight needs a queued successor pipeline. An unused
	// 16 MiB learned ceiling must not force every actor event to scan it.
	return min(uint64(runtimeLogicalBytes-runtimeInteractiveReserve), min(l.stripe.delivery.window, l.stripe.delivery.bulk)+8*datapath.BulkBatchTargetBytes)
}
