package mailstream

// Message count is independent of the byte ledger: tiny control messages must
// not accumulate indefinitely. All fields below are owned by Account.mu.
const controlMessageLimit = 2048
const controlRecoveryMessages = 128
const controlCollectionMessages = controlMessageLimit * 3 / 4
const controlCollectionBytes = 1 << 20

func (a *mailAccountState) controlMessageReservationsLocked() uint64 {
	m := &a.maintenance
	return uint64(m.initialMessages) + uint64(len(m.writes)) + uint64(m.pendingMessages) + uint64(a.controlQueued)
}

// Leave a quarter of the hard message budget for collection to catch up.
// Smaller tails remain eligible during quiet periods without taking bulk DATA
// cleanup turns merely because a few hundred tiny messages use 256 KiB.
func (a *mailAccountState) controlMessagePressureLocked() bool {
	return a.controlMessageReservationsLocked() >= controlCollectionMessages || len(a.maintenance.reads) >= controlCollectionMessages
}

func (a *mailAccountState) reserveControlPublicationLocked(packets int) {
	a.controlQueued -= packets
	// A reservation is replaced by one attempted container, even if the APPEND
	// result becomes unknown. Only a tagged UID or a fresh startup STATUS/audit
	// can release it; a transport error is not a message-deletion proof.
	a.maintenance.pendingMessages++
}
