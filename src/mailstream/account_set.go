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
		// Older authenticated proposals can arrive after the newer grant has
		// committed and removed obsolete paths from the union.
		l.mu.Lock()
		stale := f.offset <= l.remoteActiveVersion
		l.mu.Unlock()
		if stale {
			return s.trySend(frame{kind: frameAccountSetAck, offset: f.offset}, true)
		}
		ids := strings.Split(string(f.data), ",")
		if len(ids) < int(s.minimumActive.Load()) {
			return ErrCapacity
		}
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
	if len(ids) < int(s.minimumActive.Load()) {
		return ErrCapacity // never park more accounts to satisfy a sub-floor proposal
	}
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
	// Recover only essential paths serially on a congested bearer. Normal
	// operation still uses the provider login semaphore and parallel attach.
	if l.mobileRecovery.Load() {
		for _, name := range ids {
			if err := l.account(name).ensureActive(ctx, s.cipher.id); err != nil {
				return err
			}
		}
	} else {
		workCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		results := make(chan error, len(ids))
		for _, name := range ids {
			a := l.account(name)
			go func() { results <- a.ensureActive(workCtx, s.cipher.id) }()
		}
		var first error
		for range ids {
			if err := <-results; err != nil && first == nil {
				first = err
				cancel()
			}
		}
		if first != nil {
			return first
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

// SetMinimumActiveAccounts is configured by the production client after authentication.
// Low-level protocol tests may still exercise smaller synthetic account sets.
func (s *Session) SetMinimumActiveAccounts(minimum int) {
	s.accountSetMu.Lock()
	defer s.accountSetMu.Unlock()
	s.minimumActive.Store(int32(max(0, minimum)))
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
	minimum := max(1, int(s.minimumActive.Load()))
	if len(grant) < minimum || (limit != 0 && limit < minimum) {
		return ErrCapacity
	}
	requested := limit
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
	if l.mobileRecovery.Load() && l.servingCount() >= minimum {
		kept := ids[:0]
		for _, name := range ids {
			a := l.account(name)
			a.mu.Lock()
			ready := a.ready && a.id == s.cipher.id && !a.failed && !a.scaleBlocked.Load()
			a.mu.Unlock()
			if ready {
				kept = append(kept, name)
			}
		}
		ids = kept
	}
	if len(ids) < minimum {
		return ErrCapacity // keep surviving paths; do not commit a smaller active set
	}
	// The controller publishes its desired limit before the asynchronous operation.
	s.activeLimit.Store(int32(requested))
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

// Desired is safe to publish from the decision loop while a grant operation is
// blocked. No provider I/O or accountSetMu is involved.
func (s *Session) SetMobileRecovery(congested bool, desired int) {
	if l, ok := s.link.(*IMAPLink); ok && !s.server {
		if l.mobileRecovery.Swap(congested) != congested {
			s.controllerEpoch.Add(1)
		}
	}
	if s.controllerLimit.Swap(int32(desired)) != int32(desired) || !s.controllerManaged.Swap(true) {
		s.controllerEpoch.Add(1)
	}
	s.controllerManaged.Store(true)
}
func (l *IMAPLink) servingCount() int {
	n := 0
	for _, a := range l.accounts {
		a.mu.Lock()
		ready := a.ready && !a.failed && a.data != nil && a.writer != nil && a.controlWriter != nil && !a.scaleBlocked.Load()
		a.mu.Unlock()
		if ready {
			n++
		}
	}
	return n
}

// An unavailable optional actor must not start a wave of TLS logins on a
// congested link. Its durable debt remains available for later reclamation.
func (l *IMAPLink) recoverySlot(ctx context.Context) (func(), error) {
	if !l.mobileRecovery.Load() {
		return func() {}, nil
	}
	for !l.recoveryMu.TryLock() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if l.mobileRecovery.Load() && l.servingCount() >= 4 {
		l.recoveryMu.Unlock()
		return nil, ErrCapacity
	}
	return l.recoveryMu.Unlock, nil
}
