package mailstream

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

var ErrStorage = errors.New("MAILSTREAM_STORAGE")

// Durable reservation precedes publication. Reserving ahead bounds crash
// uncertainty below 1 MiB/account, including the in-progress mail body, without
// fsync on each small packet. Clean close
// returns unused reservation only after the APPEND owner joins. Unknown
// attempted APPENDs remain charged. This store never opens a legacy wallet.
const reservationQuantum = 512 << 10

type retentionStore struct {
	mu     sync.Mutex
	db     *bolt.DB
	closed bool
}

func NewStoredIMAPLink(ctx context.Context, identity string, accounts []MailAccount, client bool, dir string) (*IMAPLink, error) {
	if !validIdentity(identity) || dir == "" {
		return nil, ErrProtocol
	}
	store, err := openRetention(dir, identity, client)
	if err != nil {
		return nil, err
	}
	link, err := newIMAPLink(ctx, identity, accounts, client, store)
	if err != nil {
		store.close()
		return nil, err
	}
	return link, nil
}
func openRetention(dir, identity string, client bool) (*retentionStore, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, ErrStorage
	}
	direction := "s2c"
	if client {
		direction = "c2s"
	}
	binding := identity + "/" + direction
	path := filepath.Join(dir, "mailstream-"+identity+"-"+direction+".db")
	info, statErr := os.Lstat(path)
	existed := statErr == nil
	if existed && !info.Mode().IsRegular() || statErr != nil && !os.IsNotExist(statErr) {
		return nil, ErrStorage
	}
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, ErrStorage
	}
	if existed {
		err = db.View(func(tx *bolt.Tx) error {
			bucket := tx.Bucket([]byte("mailstream-retention-v1"))
			if bucket == nil || !bytes.Equal(bucket.Get([]byte("binding")), []byte(binding)) {
				return ErrStorage
			}
			return nil
		})
	} else {
		err = db.Update(func(tx *bolt.Tx) error {
			bucket, err := tx.CreateBucket([]byte("mailstream-retention-v1"))
			if err != nil {
				return err
			}
			return bucket.Put([]byte("binding"), []byte(binding))
		})
	}
	if err != nil {
		db.Close()
		return nil, ErrStorage
	}
	return &retentionStore{db: db}, nil
}
func retentionKey(account MailAccount) []byte {
	// Routing aliases can change when an account is re-exported. They are
	// not a new physical mailbox and must never reset its charged capacity.
	identity := net.JoinHostPort(strings.ToLower(account.Host), strconv.Itoa(account.Port)) + "\x00" + strings.ToLower(account.Username)
	hash := sha256.Sum256([]byte(identity))
	return []byte("account/" + hex.EncodeToString(hash[:]))
}
func (s *retentionStore) load(key []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, ErrStorage
	}
	value := 0
	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket([]byte("mailstream-retention-v1")).Get(key)
		if raw == nil {
			return nil
		}
		if len(raw) != 8 || binary.BigEndian.Uint64(raw) > prototypePhysicalBudget {
			return ErrStorage
		}
		value = int(binary.BigEndian.Uint64(raw))
		return nil
	})
	if err != nil {
		return 0, ErrStorage
	}
	return value, nil
}
func (s *retentionStore) put(key []byte, value int) error {
	if value < 0 || value > prototypePhysicalBudget {
		return ErrStorage
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrStorage
	}
	err := s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte("mailstream-retention-v1")).Put(key, binary.BigEndian.AppendUint64(nil, uint64(value)))
	})
	if err != nil {
		return ErrStorage
	}
	return nil
}
func (s *retentionStore) close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if err := s.db.Close(); err != nil {
		return ErrStorage
	}
	return nil
}

// Account.mu serializes reservation, attempted publication and verified
// collection. No session/global protocol lock is held for storage IO.
func (a *mailAccountState) chargeLocked(n int) error {
	if n < 0 || n > prototypePhysicalBudget-a.spent {
		return ErrCapacity
	}
	wanted := a.spent + n
	if wanted > a.reserved {
		reserved := wanted
		if a.link.store != nil {
			reserved = min(prototypePhysicalBudget, ((wanted+reservationQuantum-1)/reservationQuantum)*reservationQuantum)
			if err := a.link.store.put(a.storageKey, reserved); err != nil {
				return err
			}
		}
		a.reserved = reserved
	}
	a.spent = wanted
	return nil
}
func (a *mailAccountState) refundLocked(n int) error {
	if n < 0 || n > a.spent || n > a.reserved {
		return ErrStorage
	}
	if a.link.store != nil {
		if err := a.link.store.put(a.storageKey, a.reserved-n); err != nil {
			return err
		}
	}
	a.spent -= n
	a.reserved -= n
	return nil
}
