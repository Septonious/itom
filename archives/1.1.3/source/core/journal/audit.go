package journal

import (
	"encoding/binary"

	bolt "go.etcd.io/bbolt"
	"itom.local/core/pool"
	"itom.local/core/wire"
)

// Audit reconstructs all balances from bounded durable records. It does not
// inspect IMAP and therefore never enables admission by itself.
func (s *Store) Audit() error { return s.read(s.audit) }
func (s *Store) audit(tx *bolt.Tx) error {
	m := tx.Bucket(metaBucket)
	if m == nil {
		return ErrCorrupt
	}
	if err := auditProfileMigration(tx); err != nil {
		return err
	}
	run, err := number(m, "run")
	if err != nil {
		return err
	}
	lastGrant, err := number(m, "last_grant")
	if err != nil {
		return err
	}
	lastAttempt, err := number(m, "last_attempt")
	if err != nil {
		return err
	}
	grantCount, err := number(m, "grant_count")
	if err != nil {
		return err
	}
	attemptCount, err := number(m, "attempt_count")
	if err != nil {
		return err
	}
	if grantCount > uint64(s.config.MaxGrants) || attemptCount > uint64(s.config.MaxAttempts) {
		return ErrCorrupt
	}
	grants := map[uint64]Grant{}
	userCounts := map[string][2]uint64{}
	totals := map[string]pool.Amount{}
	for _, a := range s.config.Accounts {
		totals[accountKey(a.ID)] = pool.Amount{}
		for _, u := range a.Users {
			totals[ownerKey(a.ID, u.ID)] = pool.Amount{}
		}
	}
	err = tx.Bucket(grantBucket).ForEach(func(k, v []byte) error {
		if len(k) != 8 || uint64(len(grants)) >= grantCount {
			return ErrCorrupt
		}
		var g Grant
		if e := decode(v, &g); e != nil {
			return e
		}
		if g.Ref.ID == 0 || g.Ref.ID != binary.BigEndian.Uint64(k) || g.Ref.ID > lastGrant || g.Ref.Store != s.id ||
			g.Ref.Scope.Session == (wire.ID{}) || g.Run == 0 || g.Run > run || !validAmount(g.Limit) ||
			!within(g.Used, g.Limit) || !within(g.Released, g.Used) || !g.Remote && g.Closed && g.Used == g.Released {
			return ErrCorrupt
		}
		if g.Remote {
			if !validCounter(g.Used) || !validCounter(g.Registered) || !validCounter(g.Released) || !within(g.Registered, g.Used) || !within(g.Released, g.Registered) {
				return ErrCorrupt
			}
		} else if g.Registered != (pool.Amount{}) {
			return ErrCorrupt
		}
		a, u, e := s.owner(g.Ref.Scope.Account, g.Ref.Scope.User)
		if e != nil || !within(g.Limit, u.Limit) {
			return ErrCorrupt
		}
		grants[g.Ref.ID] = g
		counts := userCounts[g.Ref.Scope.User]
		counts[0]++
		userCounts[g.Ref.Scope.User] = counts
		n := held(g)
		for _, owner := range []string{accountKey(a.ID), ownerKey(a.ID, u.ID)} {
			totals[owner] = add(totals[owner], n)
		}
		if !within(totals[accountKey(a.ID)], a.Limit) || !within(totals[ownerKey(a.ID, u.ID)], u.Limit) {
			return ErrCorrupt
		}
		return nil
	})
	if err != nil || uint64(len(grants)) != grantCount {
		return ErrCorrupt
	}
	remaining := map[uint64]pool.Amount{}
	ordinals := map[[2]uint64]bool{}
	var count uint64
	err = tx.Bucket(attemptBucket).ForEach(func(k, v []byte) error {
		if len(k) != 8 || count >= attemptCount {
			return ErrCorrupt
		}
		var a Attempt
		if e := decode(v, &a); e != nil {
			return e
		}
		g, ok := grants[a.Ref.Grant.ID]
		if !ok || a.Ref.Grant != g.Ref || a.Ref.ID == 0 || a.Ref.ID > lastAttempt || a.Ref.ID != binary.BigEndian.Uint64(k) ||
			a.Run != g.Run || a.Batch == (wire.ID{}) || a.Digest == ([32]byte{}) || a.State > Committed ||
			a.DecodedBytes == 0 || a.DecodedBytes > MaxDecoded || a.EncodedBytes < a.DecodedBytes || a.EncodedBytes > MaxEncoded ||
			!s.ownsFolder(g.Ref.Scope, a.Folder) {
			return ErrCorrupt
		}
		if g.Remote {
			k := [2]uint64{g.Ref.ID, a.Ordinal}
			if a.Ordinal == 0 || a.Ordinal > g.Registered.Messages || ordinals[k] || a.State < Unknown {
				return ErrCorrupt
			}
			ordinals[k] = true
		} else if a.Ordinal != 0 {
			return ErrCorrupt
		}
		if (a.Location.UID == 0) != (a.Location.UIDValidity == 0) || a.Location != (Location{}) && a.State != Committed {
			return ErrCorrupt
		}
		remaining[g.Ref.ID] = add(remaining[g.Ref.ID], charge(a))
		counts := userCounts[g.Ref.Scope.User]
		counts[1]++
		userCounts[g.Ref.Scope.User] = counts
		count++
		return nil
	})
	if err != nil || count != attemptCount {
		return ErrCorrupt
	}
	for id, g := range grants {
		expected := g.Used
		if g.Remote {
			expected = g.Registered
		}
		if remaining[id] != sub(expected, g.Released) {
			return ErrCorrupt
		}
	}
	var balances int
	err = tx.Bucket(balanceBucket).ForEach(func(k, v []byte) error {
		n, ok := totals[string(k)]
		if !ok || len(v) != 16 || n.Bytes != binary.BigEndian.Uint64(v) || n.Messages != binary.BigEndian.Uint64(v[8:]) {
			return ErrCorrupt
		}
		balances++
		return nil
	})
	if err != nil || balances != len(totals) {
		return ErrCorrupt
	}
	var users int
	err = tx.Bucket(countBucket).ForEach(func(k, v []byte) error {
		if !s.userIDs[string(k)] || len(v) != 16 {
			return ErrCorrupt
		}
		counts := userCounts[string(k)]
		if counts[0] != binary.BigEndian.Uint64(v) || counts[1] != binary.BigEndian.Uint64(v[8:]) ||
			counts[0] > uint64(s.config.MaxGrants/len(s.userIDs)) || counts[1] > uint64(s.config.MaxAttempts/len(s.userIDs)) {
			return ErrCorrupt
		}
		users++
		return nil
	})
	if err != nil || users != len(s.userIDs) {
		return ErrCorrupt
	}
	return nil
}
