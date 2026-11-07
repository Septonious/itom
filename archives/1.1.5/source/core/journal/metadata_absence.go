package journal

import (
	bolt "go.etcd.io/bbolt"
	"itom.local/core/wire"
)

// GrantMetadataAbsent identifies an absent row below the original durable
// allocation high-water mark. It proves neither the row's final spend nor
// physical cleanup. Its only transport use is discarding obsolete aggregate
// control snapshots WITHOUT a status acknowledgement, refund or new grant.
func (s *Store) GrantMetadataAbsent(user string, run uint64, k wire.QuotaKey) (bool, error) {
	var absent bool
	err := s.read(func(tx *bolt.Tx) error {
		if run != s.run {
			return ErrGeneration
		}
		if k.Store != s.id || k.User != user || k.Epoch == 0 || k.Epoch > run || k.Grant == 0 {
			return ErrScope
		}
		if _, _, err := s.owner(k.Account, user); err != nil {
			return err
		}
		last, err := number(tx.Bucket(metaBucket), "last_grant")
		if err != nil {
			return err
		}
		if k.Grant > last {
			return ErrScope
		}
		absent = tx.Bucket(grantBucket).Get(key(k.Grant)) == nil
		return nil
	})
	return absent, err
}
