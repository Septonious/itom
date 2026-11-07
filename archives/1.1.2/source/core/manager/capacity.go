package manager

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// VerifyCapacityState checks stopped transport ledgers without changing debt.
// A stop, update, failed purge or first start is not proof of mailbox deletion.
// Only server-owned collection may refund physical retention. The database lock
// prevents verification from racing a live transport.
func VerifyCapacityState(state string) error {
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
			db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: time.Second, ReadOnly: true})
			if err != nil {
				return errors.New("capacity ledger is busy or unreadable; stop the service first")
			}
			err = db.View(func(tx *bolt.Tx) error {
				b := tx.Bucket([]byte("mailstream-retention-v1"))
				if b == nil || !bytes.Equal(b.Get([]byte("binding")), []byte(identity+"/s2c")) {
					return errors.New("capacity ledger binding is invalid")
				}
				if err := b.ForEach(func(k, v []byte) error {
					if bytes.HasPrefix(k, []byte("account/")) {
						if len(v) != 8 {
							return errors.New("capacity ledger entry is invalid")
						}
					}
					return nil
				}); err != nil {
					return err
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
