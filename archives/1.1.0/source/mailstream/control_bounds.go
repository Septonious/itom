package mailstream

// Message count is independent of the byte ledger: tiny control messages must
// not accumulate indefinitely. All fields below are owned by Account.mu.
const controlMessageLimit = 2048
const controlRecoveryMessages = 128

func (a *mailAccountState) controlMessageReservationsLocked() uint64 {
	m := &a.maintenance
	return uint64(m.initialMessages) + uint64(len(m.writes)) + uint64(m.pendingMessages) + uint64(a.controlQueued)
}

// Start collection with half the ordinary budget still available. Audited
// initial-UID spans release this urgency page by page, so neither stale startup
// counts nor a last-minute backlog can starve reusable DATA capacity.
func (a *mailAccountState) controlMessagePressureLocked() bool {
	return a.controlMessageReservationsLocked() >= controlMessageLimit/2 || len(a.maintenance.reads) >= controlMessageLimit/2
}

func (a *mailAccountState) reserveControlPublicationLocked(packets int) {
	a.controlQueued -= packets
	// A reservation is replaced by one attempted container, even if the APPEND
	// result becomes unknown. Only a tagged UID or a fresh startup STATUS/audit
	// can release it; a transport error is not a message-deletion proof.
	a.maintenance.pendingMessages++
}
