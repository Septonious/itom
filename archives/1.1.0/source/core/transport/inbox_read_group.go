package transport

// readGroup uses only already enumerated UIDs and RFC822.SIZE hints. A hint is
// not trusted by the parser or any decoder: original byte, replay, per-ledger
// admission and accounting checks still run for each complete BODY. Missing or
// oversized hints retain single-body service. There is no collector delay.
func (i *Inbox) readGroup(uid uint32, remaining []uint32, sizes map[uint32]Body, available inboxAdmission) []uint32 {
	group := []uint32{uid}
	if i.limits.BodyReadBatch < 2 || i.stopAfterApply != nil || !available.data || i.admit != nil && !available.control {
		return group
	}
	room := i.limits.PendingDeliveries
	for _, d := range i.pending {
		if deliveryClass(d) == DataLedger {
			room--
		}
	}
	if room < 2 {
		return group
	}
	first := sizes[uid]
	if !first.SizeKnown || first.Size > uint32(i.executor.limits.LiteralBytes) {
		return group
	}
	// Reserve conservative framing for BOTH header and trailer per literal.
	// This is below the original aggregate response limit, never a larger one.
	used := uint64(first.Size) + 2*uint64(i.executor.limits.LineBytes)
	for _, next := range remaining {
		if i.seen[next] || i.pending[next] != nil || i.examined[next] {
			continue
		}
		if class, blocked := i.deferred[next]; blocked && !available.allows(class) {
			continue
		}
		size := sizes[next]
		if !size.SizeKnown || size.Size > uint32(i.executor.limits.LiteralBytes) {
			break
		}
		used += uint64(size.Size) + 2*uint64(i.executor.limits.LineBytes)
		if used <= uint64(i.executor.limits.ResponseBytes) {
			group = append(group, next)
		}
		break // at most two UIDs; never skip around a large ready body
	}
	return group
}
