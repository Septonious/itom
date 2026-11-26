package mailstream

import "context"

// installInitialGrant is deliberately restricted to the pre-DATA handshake.
// No live account is stopped: its DATA generation and UID cursor cannot be
// safely reconstructed by calling attach again. Server control readers remain
// warm. Client bootstrap-only readers outside the authenticated grant exit.
func (l *IMAPLink) installInitialGrant(ctx context.Context, id SessionID, ids []string) error {
	if id == (SessionID{}) || len(ids) == 0 || len(ids) > len(l.accounts) {
		return ErrProtocol
	}
	allowed := make(map[string]bool, len(ids))
	for _, name := range ids {
		if !validIdentity(name) || allowed[name] {
			return ErrProtocol
		}
		found := false
		for _, a := range l.accounts {
			if a.config.ID == name && !a.unallocated.Load() {
				found = true
				break
			}
		}
		if !found {
			return ErrProtocol
		}
		allowed[name] = true
	}
	l.mu.Lock()
	if l.attached != (SessionID{}) || l.grantSession != (SessionID{}) {
		l.mu.Unlock()
		return ErrProtocol
	}
	l.grantSession, l.grant = id, allowed
	l.mu.Unlock()
	if !l.client { return nil }
	for _, a := range l.accounts {
		if !allowed[a.config.ID] { a.unallocated.Store(true); a.cancel() }
	}
	for _, a := range l.accounts {
		if allowed[a.config.ID] { continue }
		select {
		case <-a.stopped:
		case <-ctx.Done(): return ctx.Err()
		case <-l.ctx.Done(): return ErrClosed
		}
	}
	return nil
}
