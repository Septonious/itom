package journal

import (
	bolt "go.etcd.io/bbolt"
	"itom.local/core/pool"
	"time"
)

// IssueInitialRemote is only the initial CONTROL invitation allocation. It
// deduplicates an original writer Session across simultaneous/repeated request
// observers in this issuer Run. Ordinary later grants still use IssueRemote.
// Session tombstones stay bounded even after grant retirement; a new Run has
// different authenticated rendezvous context and cannot accept an old request.
func (s *Store) IssueInitialRemote(scope Scope, allowance pool.Amount) (Grant, error) {
	queued := time.Now()
	s.mu.Lock()
	s.metrics.queueNS.Add(uint64(time.Since(queued)))
	defer s.mu.Unlock()
	if s.initialSessions[scope] || s.sessionClaims[scope] {
		return Grant{}, ErrState
	}
	if len(s.initialSessions) >= s.config.MaxGrants {
		return Grant{}, ErrLimit
	}
	var result Grant
	err := s.writeLocked(func(tx *bolt.Tx) error {
		if err := tx.Bucket(grantBucket).ForEach(func(_, raw []byte) error {
			var g Grant
			if err := decode(raw, &g); err != nil {
				return err
			}
			if g.Remote && g.Run == s.run && g.Ref.Scope == scope {
				return ErrState
			}
			return nil
		}); err != nil {
			return err
		}
		return s.issueTx(tx, scope, allowance, true, &result)
	})
	if err != nil {
		return Grant{}, err
	}
	if s.initialSessions == nil {
		s.initialSessions = make(map[Scope]bool)
	}
	s.initialSessions[scope] = true
	return result, nil
}

// ClaimUnusedRemoteSession admits at most one startup owner for a fresh remote
// session in this exclusively locked issuer Run. It neither writes a message nor
// changes held quota. Reopen advances the durable Run before admitting anyone,
// so an old invitation cannot regain this volatile claim after a process crash.
// There is no release operation: cancellation/uncertainty must preserve the
// original owner or use a genuinely new invitation, never reuse Noise/session
// state by dropping this claim. Claims are bounded by the original grant bound.
func (s *Store) ClaimUnusedRemoteSession(ref GrantRef) error {
	return s.read(func(tx *bolt.Tx) error {
		g, err := s.grant(tx, ref)
		if err != nil {
			return err
		}
		if !g.Remote || g.Closed || g.Run != s.run {
			return ErrGeneration
		}
		if !s.ready[ownerKey(ref.Scope.Account, ref.Scope.User)] {
			return ErrRecovery
		}
		if s.sessionClaims[ref.Scope] || g.Used != (pool.Amount{}) || g.Registered != (pool.Amount{}) || g.Released != (pool.Amount{}) {
			return ErrState
		}
		if len(s.sessionClaims) >= s.config.MaxGrants {
			return ErrLimit
		}
		if s.sessionClaims == nil {
			s.sessionClaims = make(map[Scope]bool)
		}
		s.sessionClaims[ref.Scope] = true
		return nil
	})
}
