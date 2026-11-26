package mailstream

import (
	"context"
	"errors"
	"time"
)

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
	l.stableGrant = allowed
	// Until the peer acknowledges a new active set, ordinary traffic must
	// continue to use the original grant, including grant-update frames.
	if !l.client {
		l.remoteActive = allowed
	}
	l.mu.Unlock()
	if !l.client {
		return nil
	}
	// Mark unallocated before canceling an in-progress bootstrap attempt.
	// The account actor stays alive and can be reactivated without resetting
	// its retained mailbox state when a later authenticated grant expands.
	for _, a := range l.accounts {
		if allowed[a.config.ID] {
			continue
		}
		a.unallocated.Store(true)
		a.scaleBlocked.Store(true)
		a.mu.Lock()
		stop := a.startupStop
		a.mu.Unlock()
		if stop != nil {
			stop()
		}
		select {
		case a.startupWake <- struct{}{}:
		default:
		}
	}
	results := make(chan error, len(l.accounts))
	n := 0
	for _, a := range l.accounts {
		if allowed[a.config.ID] {
			continue
		}
		n++
		go func() {
			for {
				err := a.scale(ctx, false)
				if !errors.Is(err, ErrCapacity) {
					if a.ctx.Err() != nil {
						err = nil
					}
					results <- err
					return
				}
				select {
				case <-ctx.Done():
					results <- ctx.Err()
					return
				case <-time.After(10 * time.Millisecond):
				}
			}
		}()
	}
	for range n {
		select {
		case err := <-results:
			if err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		case <-l.ctx.Done():
			return ErrClosed
		}
	}
	return nil
}
