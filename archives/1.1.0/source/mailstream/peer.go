package mailstream

import (
	"bytes"
	"context"
	"time"
)

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

// accountRoute is local attachment state, not a claim about the remote peer.
// The authenticated announcement itself must travel on that account.
type accountRoute struct {
	account   string
	confirmed bool
}
type accountAnnouncer interface {
	readyRoutes(SessionID) []accountRoute
	sendRoute(context.Context, SessionID, string, []byte) error
}

func (l *IMAPLink) readyRoutes(id SessionID) []accountRoute {
	l.mu.Lock()
	confirmed := make(map[string]bool, len(l.peers))
	for k, v := range l.peers {
		confirmed[k] = v
	}
	current := l.attached
	l.mu.Unlock()
	if current != id {
		return nil
	}
	var routes []accountRoute
	for _, a := range l.accounts {
		a.mu.Lock()
		ready := a.ready && !a.failed && a.id == id
		a.mu.Unlock()
		if ready {
			routes = append(routes, accountRoute{a.config.ID, confirmed[a.config.ID]})
		}
	}
	return routes
}
func (l *IMAPLink) sendRoute(ctx context.Context, id SessionID, account string, wire []byte) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	for _, a := range l.accounts {
		if a.config.ID != account {
			continue
		}
		work := mailWork{body: bytes.Clone(wire), control: true, charge: len(wire)*3/2 + 512}
		a.mu.Lock()
		if !a.ready || a.failed || a.id != id {
			a.mu.Unlock()
			return ErrClosed
		}
		if a.spent+a.queuedBytes+work.charge > prototypePhysicalBudget {
			a.mu.Unlock()
			return ErrCapacity
		}
		select {
		case a.fast <- work:
			a.queuedBytes += work.charge
			a.mu.Unlock()
			a.sentPacket(wire)
			return nil
		default:
			a.mu.Unlock()
			return ErrCapacity
		}
	}
	return ErrClosed
}

// Route announcements do not depend on application traffic or on bulk storage
// credit. Otherwise a full first account can prevent discovery of a free one.
// Retry the same ciphertext until remote traffic proves the account is joined.
func (s *Session) announceRoutes(link accountAnnouncer) {
	type announcement struct {
		wire []byte
		sent bool
	}
	announcements := map[string]*announcement{}
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		for _, route := range link.readyRoutes(s.cipher.id) {
			old := announcements[route.account]
			if old != nil && old.sent && route.confirmed {
				continue
			}
			if old == nil {
				plain, _ := (frame{kind: frameRouteReady}).encode()
				wire, _, err := s.cipher.seal(plain)
				if err != nil {
					s.fail(err)
					return
				}
				old = &announcement{wire: wire}
				announcements[route.account] = old
			}
			if err := link.sendRoute(s.ctx, s.cipher.id, route.account, old.wire); err == nil {
				old.sent = true
			}
		}
		select {
		case <-s.ctx.Done():
			return
		case <-tick.C:
		}
	}
}
