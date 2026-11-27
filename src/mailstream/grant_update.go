package mailstream

import (
	"context"
	"slices"
	"strings"
	"time"
)

type grantRequest struct {
	version uint64
	ids     []string
}

func (l *IMAPLink) knownGrant(ids []string) (map[string]bool, error) {
	if len(ids) == 0 || len(ids) > len(l.accounts) {
		return nil, ErrProtocol
	}
	set := make(map[string]bool, len(ids))
	for _, name := range ids {
		if !validIdentity(name) || set[name] || l.account(name) == nil {
			return nil, ErrProtocol
		}
		set[name] = true
	}
	return set, nil
}
func (l *IMAPLink) unionGrant(id SessionID, desired map[string]bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.attached != id || l.grantSession != id {
		return ErrProtocol
	}
	union := make(map[string]bool, len(l.grant)+len(desired))
	for a := range l.grant {
		union[a] = true
	}
	for a := range desired {
		union[a] = true
	}
	l.grant = union
	l.grantChanging = true
	return nil
}
func (l *IMAPLink) commitGrant(id SessionID, desired map[string]bool) {
	l.mu.Lock()
	if l.grantSession == id {
		l.grant = desired
		l.stableGrant = desired
		l.grantChanging = false
	}
	l.mu.Unlock()
}
func (s *Session) applyGrantFrame(f frame) error {
	l, ok := s.link.(*IMAPLink)
	if !ok || f.stream != 0 || f.offset == 0 {
		return ErrProtocol
	}
	switch f.kind {
	case frameGrantAck:
		if !s.server || len(f.data) != 0 {
			return ErrProtocol
		}
		for old := s.grantAck.Load(); old < f.offset; old = s.grantAck.Load() {
			if s.grantAck.CompareAndSwap(old, f.offset) {
				break
			}
		}
		return nil
	case frameGrantUpdate:
		if s.server || s.grantRequests == nil {
			return ErrProtocol
		}
		ids := strings.Split(string(f.data), ",")
		if _, err := l.knownGrant(ids); err != nil {
			return err
		}
		if f.offset <= s.grantRequestVersion {
			return nil
		}
		s.grantRequestVersion = f.offset
		req := grantRequest{f.offset, ids}
		select {
		case <-s.grantRequests:
		default:
		}
		s.grantRequests <- req
		return nil
	}
	return ErrProtocol
}

// Server keeps standing control readers and previously attached DATA paths
// warm. Make-before-break: admit union during negotiation, then commit only
// after the peer has acknowledged its physical reconfiguration.
func (s *Session) updateGrant(ctx context.Context, ids []string) error {
	l, ok := s.link.(*IMAPLink)
	if !ok || !s.server {
		return ErrProtocol
	}
	s.grantUpdateMu.Lock()
	defer s.grantUpdateMu.Unlock()
	desired, err := l.knownGrant(ids)
	if err != nil {
		return err
	}
	l.mu.Lock()
	same := !l.grantChanging && len(l.stableGrant) == len(desired)
	for name := range desired {
		same = same && l.stableGrant[name]
	}
	l.mu.Unlock()
	// A prior failed union must still be negotiated, even if it happens to
	// equal the newest desired set. Do not skip an unacknowledged version.
	if same && s.grantAck.Load() == s.grantVersion {
		return nil
	}
	if err = l.unionGrant(s.cipher.id, desired); err != nil {
		return err
	}
	results := make(chan error, len(ids))
	for _, name := range ids {
		a := l.account(name)
		go func() { results <- a.ensureActive(ctx, s.cipher.id) }()
	}
	for range ids {
		select {
		case e := <-results:
			if e != nil {
				return e
			}
		case <-ctx.Done():
			return ctx.Err()
		case <-s.ctx.Done():
			return ErrClosed
		}
	}
	s.grantVersion++
	version := s.grantVersion
	if err = s.send(ctx, frame{kind: frameGrantUpdate, offset: version, data: []byte(strings.Join(ids, ","))}, true); err != nil {
		return err
	}
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for s.grantAck.Load() < version {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.ctx.Done():
			return ErrClosed
		case <-tick.C:
		}
	}
	l.commitGrant(s.cipher.id, desired)
	return nil
}
func (s *Session) startGrantUpdates(get func() []string) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-tick.C:
				ids := slices.Clone(get())
				ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
				// A superseded proposal must not monopolize the negotiation
				// for its entire acknowledgement timeout. Preserve the union
				// and start the newest version after the current owner exits.
				joined := make(chan struct{})
				go func() {
					defer close(joined)
					watch := time.NewTicker(250 * time.Millisecond)
					defer watch.Stop()
					for {
						select {
						case <-ctx.Done():
							return
						case <-watch.C:
							if !slices.Equal(ids, get()) {
								cancel()
								return
							}
						}
					}
				}()
				_ = s.updateGrant(ctx, ids)
				cancel()
				<-joined
			}
		}
	}()
}
func (s *Session) grantWorker() {
	var pending *grantRequest
	for {
		if pending == nil {
			select {
			case <-s.ctx.Done():
				return
			case req := <-s.grantRequests:
				pending = &req
			}
		}
		ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
		err := s.applyLiveGrant(ctx, *pending)
		cancel()
		if err == nil {
			pending = nil
			continue
		}
		select {
		case <-s.ctx.Done():
			return
		case req := <-s.grantRequests:
			pending = &req
		case <-time.After(time.Second):
		}
	}
}
func (s *Session) applyLiveGrant(ctx context.Context, req grantRequest) error {
	l, ok := s.link.(*IMAPLink)
	if !ok {
		return ErrProtocol
	}
	s.accountSetMu.Lock()
	defer s.accountSetMu.Unlock()
	desired, err := l.knownGrant(req.ids)
	if err != nil {
		return err
	}
	if err = l.unionGrant(s.cipher.id, desired); err != nil {
		return err
	}
	for _, a := range l.accounts {
		if desired[a.config.ID] {
			a.unallocated.Store(false)
		}
	}
	// Keep established paths first when a mobile controller limits the count.
	limit := int(s.activeLimit.Load())
	if limit <= 0 || limit > len(req.ids) {
		limit = len(req.ids)
	}
	target := make([]string, 0, limit)
	for _, ready := range []bool{true, false} {
		for _, name := range req.ids {
			a := l.account(name)
			a.mu.Lock()
			serving := a.ready && a.id == s.cipher.id && !a.scaleBlocked.Load()
			a.mu.Unlock()
			if serving == ready && len(target) < limit {
				target = append(target, name)
			}
		}
	}
	// Publish the intended active paths even during failed preparation.
	// This allows the server to replace a dead candidate in an unfinished grant.
	healthTarget := make(map[string]bool, len(target))
	for _, name := range target {
		healthTarget[name] = true
	}
	l.mu.Lock()
	l.healthTarget = healthTarget
	l.mu.Unlock()
	if err = s.setActiveAccountsLocked(ctx, l, target); err != nil {
		return err
	}
	l.commitGrant(s.cipher.id, desired)
	l.mu.Lock()
	l.healthTarget = nil
	l.mu.Unlock()
	for _, a := range l.accounts {
		if !desired[a.config.ID] {
			a.unallocated.Store(true)
			a.scaleBlocked.Store(true)
			a.mu.Lock()
			if !a.ready {
				a.stage = "unallocated"
			}
			a.mu.Unlock()
		}
	}
	return s.send(ctx, frame{kind: frameGrantAck, offset: req.version}, true)
}
