package manager

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// ResetCapacityState is called only after service shutdown, or before the
// first start of the explicit-reset policy. It never edits profiles, logins,
// server identity, provider messages, or the retired transport's databases.
// bbolt's exclusive lock prevents a reset from racing a live transport.
func ResetCapacityState(state string) error {
	for _, dir := range []string{state, filepath.Join(state, "tunnel-server")} {
		files, err := filepath.Glob(filepath.Join(dir, "mailstream-*-s2c.db"))
		if err != nil {
			return err
		}
		for _, path := range files {
			info, err := os.Lstat(path)
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return errors.New("capacity ledger is not a regular file")
			}
			identity := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "mailstream-"), "-s2c.db")
			db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: time.Second})
			if err != nil {
				return errors.New("capacity ledger is busy or unreadable; stop the service first")
			}
			err = db.Update(func(tx *bolt.Tx) error {
				b := tx.Bucket([]byte("mailstream-retention-v1"))
				if b == nil || !bytes.Equal(b.Get([]byte("binding")), []byte(identity+"/s2c")) {
					return errors.New("capacity ledger binding is invalid")
				}
				var keys [][]byte
				if err := b.ForEach(func(k, v []byte) error {
					if bytes.HasPrefix(k, []byte("account/")) {
						if len(v) != 8 {
							return errors.New("capacity ledger entry is invalid")
						}
						keys = append(keys, bytes.Clone(k))
					}
					return nil
				}); err != nil {
					return err
				}
				for _, key := range keys {
					if err := b.Put(key, binary.BigEndian.AppendUint64(nil, 0)); err != nil {
						return err
					}
				}
				return nil
			})
			err = errors.Join(err, db.Close())
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// An update performed by an older manager does not yet run the new stop hook.
// One migration reset makes that first RC4 start obey the update policy too.
// Subsequent crash restarts preserve their charged capacity.
func PrepareCapacityPolicy(state string) error {
	marker := filepath.Join(state, "capacity-reset-policy-v1")
	if info, err := os.Lstat(marker); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("invalid capacity policy marker")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := ResetCapacityState(state); err != nil {
		return err
	}
	return atomicFile(marker, []byte("explicit stop/restart/update/purge resets local server capacity\n"), 0600)
}
