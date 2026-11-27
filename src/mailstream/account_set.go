package mailstream

import (
	"context"
	"strings"
	"time"
)

// Active-account changes use authenticated frames, are monotonic, and retain
// the session cipher and stream state. Only the client initiates this message.
func (s *Session) applyAccountSet(f frame) error {
	l, ok := s.link.(*IMAPLink)
	if !ok || f.stream != 0 || f.offset == 0 {
		return ErrProtocol
	}
	switch f.kind {
	case frameAccountSetAck:
		if s.server || len(f.data) != 0 {
			return ErrProtocol
		}
		for old := s.accountSetAck.Load(); old < f.offset; old = s.accountSetAck.Load() {
			if s.accountSetAck.CompareAndSwap(old, f.offset) {
				break
			}
		}
		return nil
	case frameAccountSet:
		if !s.server || len(f.data) == 0 {
			return ErrProtocol
		}
		ids := strings.Split(string(f.data), ",")
		allowed, err := l.validateActiveSet(s.cipher.id, ids)
		if err != nil {
			return err
		}
		l.mu.Lock()
		if f.offset > l.remoteActiveVersion {
			l.remoteActive = allowed
			l.remoteActiveVersion = f.offset
		}
		l.mu.Unlock()
		return s.trySend(frame{kind: frameAccountSetAck, offset: f.offset}, true)
	}
	return ErrProtocol
}
func (l *IMAPLink) validateActiveSet(id SessionID, ids []string) (map[string]bool, error) {
	if len(ids) == 0 || len(ids) > len(l.accounts) {
		return nil, ErrProtocol
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.attached != id || l.grantSession != id {
		return nil, ErrProtocol
	}
	allowed := make(map[string]bool, len(ids))
	for _, name := range ids {
		if !l.grant[name] || allowed[name] {
			return nil, ErrProtocol
		}
		allowed[name] = true
	}
	return allowed, nil
}

// SetActiveAccounts changes actual physical socket count on the client, not
// the grant. Existing sockets are drained by their owners before closing.
// Newly opened accounts retain their original reader checkpoint and writer
// generation. Callers must retry a bounded unsuccessful park operation.
func (s *Session) SetActiveAccounts(ctx context.Context, ids []string) error {
	l, ok := s.link.(*IMAPLink)
	if !ok || s.server {
		return ErrProtocol
	}
	s.accountSetMu.Lock()
	defer s.accountSetMu.Unlock()
	return s.setActiveAccountsLocked(ctx, l, ids)
}
func (s *Session) setActiveAccountsLocked(ctx context.Context, l *IMAPLink, ids []string) error {
	allowed, err := l.validateActiveSet(s.cipher.id, ids)
	if err != nil {
		return err
	}
	// Do not launch a new wave of logins for a proposal already known to
	// contain a permanently failed actor. The health reporter requests a
	// replacement via a surviving path.
	for _, name := range ids {
		a := l.account(name)
		a.mu.Lock()
		failed := a.failed
		a.mu.Unlock()
		if failed {
			return ErrClosed
		}
	}
	// Login concurrently, bounded by the shared provider startup semaphore.
	results := make(chan error, len(ids))
	for _, name := range ids {
		a := l.account(name)
		go func() { results <- a.ensureActive(ctx, s.cipher.id) }()
	}
	for range ids {
		select {
		case err := <-results:
			if err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		case <-s.ctx.Done():
			return ErrClosed
		}
	}
	s.accountSetVersion++
	revision := s.accountSetVersion
	if err = s.send(ctx, frame{kind: frameAccountSet, offset: revision, data: []byte(strings.Join(ids, ","))}, true); err != nil {
		return err
	}
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for s.accountSetAck.Load() < revision {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.ctx.Done():
			return ErrClosed
		case <-tick.C:
		}
	}
	for _, a := range l.accounts {
		if !a.unallocated.Load() {
			a.scaleBlocked.Store(!allowed[a.config.ID])
		}
	}
	parked := make(chan error, len(l.accounts))
	n := 0
	for _, a := range l.accounts {
		if allowed[a.config.ID] || a.unallocated.Load() {
			continue
		}
		a.mu.Lock()
		ready := a.ready
		a.mu.Unlock()
		if !ready {
			continue
		}
		n++
		go func() {
			for {
				e := a.scale(ctx, false)
				if e != ErrCapacity {
					parked <- e
					return
				}
				select {
				case <-ctx.Done():
					parked <- ctx.Err()
					return
				case <-s.ctx.Done():
					parked <- ErrClosed
					return
				case <-time.After(10 * time.Millisecond):
				}
			}
		}()
	}
	var first error
	for range n {
		select {
		case e := <-parked:
			if e != nil && first == nil {
				first = e
			}
		case <-ctx.Done():
			return ctx.Err()
		case <-s.ctx.Done():
			return ErrClosed
		}
	}

	return first
}

// ensureActive never calls attach on a parked DATA generation: its reader is
// resumed by the writer actor. A genuinely new account may attach once.
func (a *mailAccountState) ensureActive(ctx context.Context, id SessionID) error {
	a.mu.Lock()
	ready, failed, stage, current := a.ready, a.failed, a.stage, a.id
	a.mu.Unlock()
	if failed {
		return ErrClosed
	}
	if !ready && (stage == "parked" || stage == "unallocated") {
		if err := a.scale(ctx, true); err != nil {
			return err
		}
	}
	if current == id && ready {
		return nil
	}
	for {
		a.mu.Lock()
		ready, failed, current = a.ready, a.failed, a.id
		a.mu.Unlock()
		if failed {
			return ErrClosed
		}
		if ready && current == id {
			return nil
		}
		if ready && a.link.peerAllows(a.config.ID, id) {
			if err := a.attach(id); err == nil {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-a.ctx.Done():
			return ErrClosed
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// SetActiveLimit chooses healthy already-active paths first, without changing
// the authenticated allocation. A zero limit means all assigned accounts.
func (s *Session) SetActiveLimit(ctx context.Context, limit int) error {
	l, ok := s.link.(*IMAPLink)
	if !ok || s.server || limit < 0 {
		return ErrProtocol
	}
	s.accountSetMu.Lock()
	defer s.accountSetMu.Unlock()
	l.mu.Lock()
	if l.grantSession != s.cipher.id || l.grantChanging {
		l.mu.Unlock()
		return ErrCapacity
	}
	grant := l.stableGrant
	l.mu.Unlock()
	s.activeLimit.Store(int32(limit))
	if limit == 0 || limit > len(grant) {
		limit = len(grant)
	}
	ids := make([]string, 0, limit)
	for _, active := range []bool{true, false} {
		for _, a := range l.accounts {
			if !grant[a.config.ID] {
				continue
			}
			a.mu.Lock()
			ready := a.ready && a.id == s.cipher.id && !a.scaleBlocked.Load()
			failed := a.failed
			a.mu.Unlock()
			if !failed && ready == active && len(ids) < limit {
				ids = append(ids, a.config.ID)
			}
		}
	}
	if len(ids) == 0 {
		return ErrCapacity
	}
	target := map[string]bool{}
	for _, id := range ids {
		target[id] = true
	}
	same := true
	for _, a := range l.accounts {
		a.mu.Lock()
		ready := a.ready && a.id == s.cipher.id
		a.mu.Unlock()
		if ready != target[a.config.ID] || (ready && a.scaleBlocked.Load()) {
			same = false
			break
		}
	}
	if same {
		return nil
	}
	return s.setActiveAccountsLocked(ctx, l, ids)
}
