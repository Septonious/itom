package journal

import (
	"bytes"
	"encoding/binary"
	bolt "go.etcd.io/bbolt"
	"itom.local/core/pool"
	"itom.local/core/wire"
)

type originAmounts struct{ local, remote pool.Amount }
type originGrantView struct {
	raw   []byte
	grant Grant
}

// Called only inside Store.read with the store mutex and a live bbolt snapshot.
// Reuse a decode only when the EXACT persisted row is unchanged. The copy is
// owned here: a bbolt page may be reused after the transaction ends. A new
// transaction still rebuilds origin totals, including all uncertain/old debt.
// This is a bounded read optimization; durable admission/refund never uses it.
func (s *Store) originGrant(k, raw []byte) (Grant, error) {
	if len(k) != 8 {
		return Grant{}, ErrCorrupt
	}
	id := binary.BigEndian.Uint64(k)
	if v, ok := s.originGrants[id]; ok && bytes.Equal(v.raw, raw) {
		return v.grant, nil
	}
	var g Grant
	if err := decode(raw, &g); err != nil {
		return Grant{}, err
	}
	if len(s.originGrants) >= min(s.config.MaxGrants, 256) {
		clear(s.originGrants)
	}
	if s.originGrants == nil {
		s.originGrants = make(map[uint64]originGrantView)
	}
	s.originGrants[id] = originGrantView{raw: bytes.Clone(raw), grant: g}
	return g, nil
}

type originSession struct {
	owner   string
	session wire.ID
}

// OriginUsage separates existing physical debt from this cipher's allocations.
// Historical charges remain in Local/Remote; no amount here authorizes refund.
type OriginUsage struct{ Local, Remote, HistoricalLocal, HistoricalRemote pool.Amount }

func (s *Store) UsageByOrigin(account, user string, current wire.ID) (v OriginUsage, err error) {
	err = s.read(func(tx *bolt.Tx) error {
		if _, _, e := s.owner(account, user); e != nil {
			return e
		}
		if e := s.refreshOriginView(tx); e != nil {
			return e
		}
		owner := ownerKey(account, user)
		all := s.originSnapshot[owner]
		now := s.originSessions[originSession{owner, current}]
		if !within(now.local, all.local) || !within(now.remote, all.remote) {
			return ErrCorrupt
		}
		v = OriginUsage{Local: all.local, Remote: all.remote, HistoricalLocal: sub(all.local, now.local), HistoricalRemote: sub(all.remote, now.remote)}
		return nil
	})
	if err != nil {
		return OriginUsage{}, err
	}
	return v, nil
}
func (s *Store) refreshOriginView(tx *bolt.Tx) error {
	if s.originSnapshotID == tx.ID() && s.originSnapshot != nil {
		return nil
	}
	amounts := make(map[string]originAmounts)
	sessions := make(map[originSession]originAmounts)
	count := 0
	err := tx.Bucket(grantBucket).ForEach(func(k, raw []byte) error {
		count++
		if count > s.config.MaxGrants {
			return ErrCorrupt
		}
		g, e := s.originGrant(k, raw)
		if e != nil {
			return e
		}
		owner := ownerKey(g.Ref.Scope.Account, g.Ref.Scope.User)
		key := originSession{owner, g.Ref.Scope.Session}
		all, now := amounts[owner], sessions[key]
		if g.Remote {
			all.remote = add(all.remote, held(g))
			now.remote = add(now.remote, held(g))
		} else {
			all.local = add(all.local, held(g))
			now.local = add(now.local, held(g))
		}
		amounts[owner], sessions[key] = all, now
		return nil
	})
	if err != nil {
		return err
	}
	s.originSnapshotID, s.originSnapshot, s.originSessions = tx.ID(), amounts, sessions
	return nil
}
