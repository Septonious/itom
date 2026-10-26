package transport

import "itom.local/core/wire"

// historyScope is installed only by the persistent endpoint owner, before any
// inbox starts. It permits reading authenticated retained mail from a previous
// session without replaying it into the new Noise epoch. It has no journal,
// wallet, deletion or refund API. Unknown/foreign bodies still fail closed.
type historyScope struct {
	user    string
	session wire.ID
	key     [32]byte
}

func (h *historyScope) historical(account, folder string, envelope mailboxEnvelope) (bool, error) {
	if h == nil {
		return false, nil
	}
	u := envelope.usage
	if u.Attempt == nil || u.Key.Account != account || u.Key.User != h.user || u.Attempt.Folder != folder {
		return false, ErrProtocol
	}
	return u.Key.Session != h.session, nil
}

func installHistory(lanes []RosterLane, user string, session wire.ID, key []byte) {
	for _, lane := range lanes {
		h := &historyScope{user: user, session: session}
		copy(h.key[:], key)
		lane.Executor.history = h
	}
}
