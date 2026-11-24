package journal

import (
	"encoding/binary"
	bolt "go.etcd.io/bbolt"
	"itom.local/core/wire"
)

// Derived, bounded metadata. Rebuilt on every stopped/open transition so an
// older binary may safely ignore this bucket without leaving a stale index.
var remoteIndexBucket = []byte("remote_attempt_index")

func remoteIndexKey(grant, ordinal uint64) []byte {
	b := make([]byte, 16)
	binary.BigEndian.PutUint64(b, grant)
	binary.BigEndian.PutUint64(b[8:], ordinal)
	return b
}
func rebuildRemoteIndex(tx *bolt.Tx) error {
	if tx.Bucket(remoteIndexBucket) != nil {
		if err := tx.DeleteBucket(remoteIndexBucket); err != nil {
			return err
		}
	}
	index, err := tx.CreateBucket(remoteIndexBucket)
	if err != nil {
		return err
	}
	return tx.Bucket(attemptBucket).ForEach(func(_, raw []byte) error {
		var a Attempt
		if err := decode(raw, &a); err != nil {
			return err
		}
		if a.Ordinal == 0 {
			return nil
		}
		k := remoteIndexKey(a.Ref.Grant.ID, a.Ordinal)
		if index.Get(k) != nil {
			return ErrCorrupt
		}
		return index.Put(k, key(a.Ref.ID))
	})
}

// RemoteAttempt performs one exact lookup, then checks the original grant,
// run, account, user, session and ordinal. An absent row never proves refund.
func (s *Store) RemoteAttempt(k wire.QuotaKey, ordinal uint64) (AttemptRef, error) {
	var result AttemptRef
	err := s.read(func(tx *bolt.Tx) error { var err error; result, err = s.remoteAttemptTx(tx, k, ordinal); return err })
	return result, err
}
func (s *Store) remoteAttemptTx(tx *bolt.Tx, k wire.QuotaKey, ordinal uint64) (AttemptRef, error) {
	g, err := s.grant(tx, refFromKey(k))
	if err != nil {
		return AttemptRef{}, err
	}
	if !g.Remote || g.Run != k.Epoch || ordinal == 0 {
		return AttemptRef{}, ErrScope
	}
	index := tx.Bucket(remoteIndexBucket)
	if index == nil {
		return AttemptRef{}, ErrCorrupt
	}
	raw := index.Get(remoteIndexKey(g.Ref.ID, ordinal))
	if raw == nil {
		return AttemptRef{}, ErrMissing
	}
	if len(raw) != 8 {
		return AttemptRef{}, ErrCorrupt
	}
	ref := AttemptRef{Grant: g.Ref, ID: binary.BigEndian.Uint64(raw)}
	a, _, err := s.attempt(tx, ref)
	if err != nil {
		return AttemptRef{}, err
	}
	if a.Ordinal != ordinal {
		return AttemptRef{}, ErrCorrupt
	}
	return ref, nil
}
