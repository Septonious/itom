package journal

import (
	bolt "go.etcd.io/bbolt"
	"itom.local/core/pool"
	"itom.local/core/wire"
)

// IssueRemote reserves from the SAME balances and metadata shares as Issue.
// Return is a storage allocation, never a LOGIN or immediate APPEND permit.
// Publish only after success. A lost publication is recovered by ScanGrants;
// calling IssueRemote again allocates ANOTHER grant and must not be a retry.
func (s *Store) IssueRemote(scope Scope, limit pool.Amount) (Grant, error) {
	return s.issue(scope, limit, true)
}
func quotaKey(g Grant) wire.QuotaKey {
	return wire.QuotaKey{Store: g.Ref.Store, Epoch: g.Run, Grant: g.Ref.ID,
		Session: g.Ref.Scope.Session, User: g.Ref.Scope.User, Account: g.Ref.Scope.Account}
}
func quota(n pool.Amount) wire.Quota  { return wire.Quota{Bytes: n.Bytes, Messages: n.Messages} }
func amount(n wire.Quota) pool.Amount { return pool.Amount{Bytes: n.Bytes, Messages: n.Messages} }
func refFromKey(k wire.QuotaKey) GrantRef {
	return GrantRef{Store: k.Store, ID: k.Grant, Scope: Scope{Account: k.Account, User: k.User, Session: k.Session}}
}
func validCounter(n pool.Amount) bool {
	return n.Bytes <= 1<<40 && n.Messages <= 1<<24 && (n.Bytes == 0) == (n.Messages == 0) && n.Messages <= n.Bytes
}
func cleanupStatus(g Grant) wire.CleanupStatus {
	return wire.CleanupStatus{Key: quotaKey(g), Used: quota(g.Used), Registered: quota(g.Registered), Released: quota(g.Released), Closed: g.Closed}
}

// RemoteOffer returns the immutable original offer only in the issuing Run.
// Reopening never re-authorizes use of an old grant. Reconciliation of old
// grants is allowed, but a fresh session must receive a fresh allocation.
func (s *Store) RemoteOffer(ref GrantRef) (wire.StorageGrant, error) {
	var out wire.StorageGrant
	err := s.read(func(tx *bolt.Tx) error {
		g, e := s.grant(tx, ref)
		if e != nil {
			return e
		}
		if !g.Remote {
			return ErrScope
		}
		if g.Run != s.run {
			return ErrGeneration
		}
		if g.Closed {
			return ErrState
		}
		if !s.ready[ownerKey(ref.Scope.Account, ref.Scope.User)] {
			return ErrRecovery
		}
		out = wire.StorageGrant{Key: quotaKey(g), Limit: quota(g.Limit)}
		return nil
	})
	if err != nil {
		return wire.StorageGrant{}, err
	}
	return out, nil
}

// ApplyRemote accepts only authenticated CLIENT-origin usage/release messages.
// authenticatedUser MUST come from the Noise session binding, never the frame.
// run is the metadata job's server generation. A new authenticated session of
// the same user may reconcile an old original-session key; it cannot spend it.
// The future actor must also discard completions from its retired generations.
// Returned status is published only after fsync. It never authorizes writes.
func (s *Store) ApplyRemote(authenticatedUser string, run uint64, message wire.Control) (wire.CleanupStatus, error) {
	// Validate even typed values: callers are not required to have used a codec.
	if _, err := wire.EncodeControl(0, message); err != nil {
		return wire.CleanupStatus{}, ErrScope
	}
	var k wire.QuotaKey
	switch x := message.(type) {
	case wire.UsageReport:
		k = x.Key
	case wire.StorageRelease:
		k = x.Key
	default:
		return wire.CleanupStatus{}, ErrScope
	}
	if authenticatedUser != k.User {
		return wire.CleanupStatus{}, ErrScope
	}
	var result wire.CleanupStatus
	err := s.write(func(tx *bolt.Tx) error {
		if run != s.run {
			return ErrGeneration
		}
		g, e := s.grant(tx, refFromKey(k))
		if e != nil {
			return e
		}
		if !g.Remote || g.Run != k.Epoch {
			return ErrScope
		}
		before := g
		switch x := message.(type) {
		case wire.UsageReport:
			if x.Attempt == nil {
				e = remoteUsage(&g, amount(x.Total))
			} else {
				e = s.registerRemote(tx, &g, amount(x.Total), *x.Attempt)
			}
		case wire.StorageRelease:
			final := amount(x.Final)
			if g.Closed {
				if final != g.Used {
					return ErrOrder
				}
			} else {
				if !within(g.Used, final) || !within(final, g.Limit) {
					return ErrOrder
				}
				if e = remoteUsage(&g, final); e != nil {
					return e
				}
				if e = changeBalance(tx, g.Ref.Scope, sub(g.Limit, final), true); e != nil {
					return e
				}
				g.Used = final
				g.Closed = true
			}
		}
		if e != nil {
			return e
		}
		result = cleanupStatus(g)
		if g == before {
			// Duplicate reports cannot register rows: registerRemote only writes
			// while advancing Registered. All authority checks ran above.
			return errNoChange
		}
		return s.saveGrant(tx, g)
	})
	if err != nil {
		return wire.CleanupStatus{}, err
	}
	return result, nil
}
func remoteUsage(g *Grant, n pool.Amount) error {
	if !validCounter(n) || !within(n, g.Limit) {
		return ErrOrder
	}
	if n != g.Used && (n.Messages == g.Used.Messages || n.Bytes == g.Used.Bytes) {
		return ErrOrder
	}
	if within(n, g.Used) {
		return nil
	} // authenticated delayed duplicate
	if g.Closed || !within(g.Used, n) {
		return ErrOrder
	}
	g.Used = n
	return nil
}

// Registered is a durable contiguous prefix, not a count of current rows.
// Removed rows cannot be registered again. A gap returns ErrOrder; the sender
// resends from GC_STATUS.Registered.Messages+1 (or GC_PROOF_REQUEST.Next).
func (s *Store) registerRemote(tx *bolt.Tx, g *Grant, total pool.Amount, d wire.PhysicalDescriptor) error {
	if !s.ownsFolder(g.Ref.Scope, d.Folder) {
		return ErrScope
	}
	if total.Messages <= g.Registered.Messages {
		// Old descriptors cannot mutate state, create a row or release quota.
		// Return the current prefix so a lost acknowledgement converges.
		if !within(total, g.Registered) || (total != g.Registered && (total.Messages == g.Registered.Messages || total.Bytes == g.Registered.Bytes)) {
			return ErrOrder
		}
		return nil
	}
	if total.Messages != g.Registered.Messages+1 || total.Bytes != g.Registered.Bytes+d.EncodedBytes || !within(total, g.Limit) {
		return ErrOrder
	}
	// A prior aggregate report may run ahead. The descriptor must describe a
	// prefix of it or a strictly larger cumulative snapshot, never crossed axes.
	if !within(total, g.Used) && !within(g.Used, total) {
		return ErrOrder
	}
	if e := remoteUsage(g, total); e != nil {
		return e
	}
	if e := s.userCount(tx, g.Ref.Scope.User, false, true); e != nil {
		return e
	}
	id, e := next(tx, "last_attempt", "attempt_count", s.config.MaxAttempts)
	if e != nil {
		return e
	}
	a := Attempt{Ref: AttemptRef{Grant: g.Ref, ID: id}, Run: g.Run, Ordinal: total.Messages, Batch: d.Batch, Folder: d.Folder,
		EncodedBytes: d.EncodedBytes, DecodedBytes: d.DecodedBytes, Digest: d.Digest, State: Unknown}
	g.Registered = total
	if err := putJSON(tx.Bucket(attemptBucket), id, a); err != nil {
		return err
	}
	return tx.Bucket(remoteIndexBucket).Put(remoteIndexKey(g.Ref.ID, a.Ordinal), key(id))
}

// RemoteStatus is safe to retransmit; it carries accounting, not cleanup proof
// from a peer. Only Observe+Deleted by the trusted local scanner releases rows.
func (s *Store) RemoteStatus(ref GrantRef) (wire.CleanupStatus, error) {
	var result wire.CleanupStatus
	err := s.read(func(tx *bolt.Tx) error {
		g, e := s.grant(tx, ref)
		if e != nil {
			return e
		}
		if !g.Remote {
			return ErrScope
		}
		result = cleanupStatus(g)
		return nil
	})
	if err != nil {
		return wire.CleanupStatus{}, err
	}
	return result, nil
}

// RetireRemote is the final metadata handshake. Fully cleaned remote grants
// are retained (with zero held bytes) until this authenticated receipt arrives.
// Missing is acknowledged only as "no matching metadata", NEVER cleanup proof.
func (s *Store) RetireRemote(user string, run uint64, r wire.Retirement) (wire.Retirement, error) {
	if _, e := wire.EncodeControl(0, r); e != nil || r.Ack || r.Key.User != user {
		return wire.Retirement{}, ErrScope
	}
	err := s.write(func(tx *bolt.Tx) error {
		if run != s.run {
			return ErrGeneration
		}
		if r.Key.Store != s.id || r.Key.Epoch > s.run {
			return ErrScope
		}
		if _, _, e := s.owner(r.Key.Account, user); e != nil {
			return e
		}
		g, e := s.grant(tx, refFromKey(r.Key))
		if e == ErrMissing {
			last, e := number(tx.Bucket(metaBucket), "last_grant")
			if e != nil {
				return e
			}
			if r.Key.Grant > last {
				return ErrScope
			}
			return nil
		}
		if e != nil {
			return e
		}
		if !g.Remote || quotaKey(g) != r.Key {
			return ErrScope
		}
		if !g.Closed || g.Used != g.Registered || g.Used != g.Released || quota(g.Used) != r.Final {
			return ErrState
		}
		if e = tx.Bucket(grantBucket).Delete(key(g.Ref.ID)); e != nil {
			return e
		}
		if e = s.userCount(tx, user, true, false); e != nil {
			return e
		}
		return decrease(tx, "grant_count")
	})
	if err != nil {
		return wire.Retirement{}, err
	}
	r.Ack = true
	return r, nil
}
