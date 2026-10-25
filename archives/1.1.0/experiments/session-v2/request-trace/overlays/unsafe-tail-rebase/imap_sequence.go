package transport

// Bounded parser history may rebase a discovery hint after explicit EXPUNGE.
// It is never UID ownership, absence or deletion/refund proof. A lost history,
// count decrease without exact EXPUNGE, or deletion beyond the observed prefix
// invalidates the hint. In particular, ignoring a deletion beyond that prefix
// could hide a late lower UID whose insertion moved an old tail's sequence.
const maxSequenceChanges = 32

type sequenceChange struct {
	version  uint64
	expunged uint32
}

func (e *Executor) rebaseSequence(version, target uint64, sequence uint32) (uint32, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if target < version || target > e.sequenceVersion || target-version > maxSequenceChanges {
		return 0, false
	}
	for version < target {
		version++
		event := e.sequenceChanges[version%maxSequenceChanges]
		if event.version != version || event.expunged == 0 {
			return 0, false
		}
 if event.expunged>sequence {continue}
		sequence--
	}
	return sequence, true
}
