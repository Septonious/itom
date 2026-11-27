package mailstream

import (
	"strings"
	"time"
)

// Report client-side availability, excluding intentionally parked accounts.
// A full snapshot on change also clears failures which have recovered.
func (s *Session) reportAccountHealth(l *IMAPLink) {
	tick := time.NewTicker(4 * time.Second)
	defer tick.Stop()
	missingSince := map[string]time.Time{}
	var version uint64
	last := ""
	for {
		select {
		case <-s.ctx.Done():
			return
		case now := <-tick.C:
			l.mu.Lock()
			grant := l.stableGrant
			proposed := l.healthTarget != nil
			if proposed {
				grant = l.healthTarget
			}
			negotiated := l.grantSession == s.cipher.id
			l.mu.Unlock()
			if !negotiated {
				continue
			}
			var unavailable []string
			for _, a := range l.accounts {
				name := a.config.ID
				a.mu.Lock()
				ready := a.ready && !a.failed && a.id == s.cipher.id && a.writer != nil && a.controlWriter != nil
				failed := a.failed
				a.mu.Unlock()
				// A permanently failed actor cannot be resumed in this session,
				// even after it leaves the current or proposed grant. Keep that
				// exclusion until a new session establishes a fresh inventory.
				if failed {
					unavailable = append(unavailable, name)
					delete(missingSince, name)
					continue
				}
				if !grant[name] || (!proposed && a.scaleBlocked.Load()) {
					delete(missingSince, name)
					continue
				}
				if ready {
					delete(missingSince, name)
					continue
				}
				since := missingSince[name]
				if since.IsZero() {
					missingSince[name] = now
					since = now
				}
				if now.Sub(since) >= 12*time.Second {
					unavailable = append(unavailable, name)
				}
			}
			body := strings.Join(unavailable, ",")
			if body == last {
				continue
			}
			version++
			if s.trySend(frame{kind: frameAccountHealth, offset: version, data: []byte(body)}, true) == nil {
				last = body
			}
		}
	}
}
func (s *Session) applyAccountHealth(f frame) error {
	l, ok := s.link.(*IMAPLink)
	if !ok || !s.server || f.stream != 0 || f.offset == 0 {
		return ErrProtocol
	}
	names := map[string]bool{}
	if len(f.data) > 0 {
		for _, name := range strings.Split(string(f.data), ",") {
			if l.account(name) == nil || names[name] {
				return ErrProtocol
			}
			names[name] = true
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.grantSession == s.cipher.id && f.offset > l.remoteHealthVersion {
		l.remoteHealthVersion = f.offset
		l.remoteUnavailable = names
	}
	return nil
}
