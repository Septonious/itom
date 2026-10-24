package journal

import (
	"bytes"
	"encoding/binary"

	bolt "go.etcd.io/bbolt"
	"itom.local/core/wire"
)

// Derived lookup metadata, never authority to deliver, spend or refund. Like
// the remote index, it is rebuilt from original attempt rows on every open so
// an older binary can ignore it safely. One entry per existing local attempt.
var localIndexBucket = []byte("local_attempt_index")

func localIndexPrefix(grant uint64, batch wire.ID) []byte {
	return append(binary.BigEndian.AppendUint64(nil, grant), batch[:]...)
}
func localIndexKey(a Attempt) []byte {
	return binary.BigEndian.AppendUint64(localIndexPrefix(a.Ref.Grant.ID, a.Batch), a.Ref.ID)
}
func indexLocalAttempt(tx *bolt.Tx, a Attempt) error {
	return tx.Bucket(localIndexBucket).Put(localIndexKey(a), key(a.Ref.ID))
}
func rebuildLocalIndex(tx *bolt.Tx) error {
	if tx.Bucket(localIndexBucket) != nil {
		if err := tx.DeleteBucket(localIndexBucket); err != nil {
			return err
		}
	}
	if _, err := tx.CreateBucket(localIndexBucket); err != nil {
		return err
	}
	return tx.Bucket(attemptBucket).ForEach(func(_, raw []byte) error {
		var a Attempt
		if err := decode(raw, &a); err != nil {
			return err
		}
		if a.Ordinal != 0 {
			return nil
		}
		return indexLocalAttempt(tx, a)
	})
}

// LocalAttempt correlates an authenticated server-origin descriptor with an
// original persisted LOCAL attempt. Full account/user/session/store/run checks
// precede lookup. Missing or ambiguous matches grant no evidence or permission;
// Observe must still verify the actual BODY hash, size, folder and UID locator.
// Historical rows remain findable after reopening; their debt is unchanged.
func (s *Store) LocalAttempt(k wire.QuotaKey, batch wire.ID) (AttemptRef, error) {
	var result AttemptRef
	err := s.read(func(tx *bolt.Tx) error {
		g, err := s.grant(tx, refFromKey(k))
		if err != nil {
			return err
		}
		if g.Remote || g.Run != k.Epoch || batch == (wire.ID{}) {
			return ErrScope
		}
		index := tx.Bucket(localIndexBucket)
		if index == nil {
			return ErrCorrupt
		}
		prefix := localIndexPrefix(g.Ref.ID, batch)
		cursor := index.Cursor()
		lookup, raw := cursor.Seek(prefix)
		if !bytes.HasPrefix(lookup, prefix) {
			return ErrMissing
		}
		if len(lookup) != 32 || len(raw) != 8 || !bytes.Equal(lookup[24:], raw) {
			return ErrCorrupt
		}
		ref := AttemptRef{Grant: g.Ref, ID: binary.BigEndian.Uint64(raw)}
		if next, _ := cursor.Next(); bytes.HasPrefix(next, prefix) {
			return ErrScope
		}
		a, _, err := s.attempt(tx, ref)
		if err != nil {
			return err
		}
		if a.Ordinal != 0 || a.Batch != batch {
			return ErrCorrupt
		}
		result = ref
		return nil
	})
	if err != nil {
		return AttemptRef{}, err
	}
	return result, nil
}
