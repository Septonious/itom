package mailstream

type receivedRoute struct {
	id       SessionID
	sequence uint64
}

// ConfirmPeer is called only after authenticating the peer's ciphertext. An
// inbound control packet proves that account has joined the remote session.
// Local LOGIN alone does not prove remote recovery and DATA attachment ended.
func (l *IMAPLink) ConfirmPeer(packet []byte) {
	kind, id, seq, err := parseHeader(packet)
	if err != nil || kind != sealed {
		return
	}
	l.mu.Lock()
	account := l.receivedRoutes[receivedRoute{id, seq}]
	if account == "" {
		l.mu.Unlock()
		return
	}
	if l.peerSession != id {
		l.peerSession = id
		clear(l.peers)
	}
	fresh := !l.peers[account]
	l.peers[account] = true
	attached := l.attached == id
	l.mu.Unlock()
	if fresh && attached && l.client {
		for _, a := range l.accounts {
			if a.config.ID == account {
				go func() { _ = a.attach(id) }()
				break
			}
		}
	}
}

func (l *IMAPLink) peerAllows(account string, id SessionID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return !l.client || l.peerSession != id || len(l.peers) == 0 || l.peers[account]
}
