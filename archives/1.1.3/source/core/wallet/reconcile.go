package wallet

import (
	"encoding/binary"
	bolt "go.etcd.io/bbolt"
	"itom.local/core/wire"
)

// ReconcileOffer is called only for authenticated OLD-session allocations.
// A surviving wallet's missing record means no durable Spend was made for it
// (or the record already retired). A fresh/replaced wallet cannot attest this.
// Existing usage/descriptors are preserved; no old grant can become spendable.
func (w *Wallet) ReconcileOffer(o wire.StorageGrant) (Entry, error) {
	if _, err := wire.EncodeControl(0, o); err != nil || o.Key.Store != w.store || o.Key.User != w.user || o.Key.Session == w.session {
		return Entry{}, ErrState
	}
	var result Entry
	err := w.write(func(tx *bolt.Tx) error {
		b := tx.Bucket(grants)
		if b.Get(key(o.Key.Grant)) != nil {
			entry, err := get(b, o.Key)
			if err != nil || entry.Offer != o {
				return ErrState
			}
			entry.Fenced = true
			result = entry
			return put(b, o.Key.Grant, entry)
		}
		if !w.recovered || !w.oldSessions[o.Key.Session] {
			return ErrMissing
		}
		if b.Stats().KeyN >= MaxGrants {
			return ErrLimit
		}
		result = Entry{Offer: o, Fenced: true}
		if err := put(b, o.Key.Grant, result); err != nil {
			return err
		}
		m := tx.Bucket(meta)
		if o.Key.Grant > binary.BigEndian.Uint64(m.Get([]byte("high"))) {
			return m.Put([]byte("high"), key(o.Key.Grant))
		}
		return nil
	})
	return result, err
}
