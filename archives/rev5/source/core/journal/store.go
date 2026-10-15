package journal

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
	"itom.local/core/pool"
	"itom.local/core/wire"
)

var (
	metaBucket    = []byte("meta")
	grantBucket   = []byte("grants")
	attemptBucket = []byte("attempts")
	balanceBucket = []byte("balances")
	countBucket   = []byte("user_counts")
)

type Store struct {
	mu               sync.Mutex
	metrics          commitMetrics
	db               *bolt.DB
	config           Config
	accounts         map[string]Account
	users            map[string]User
	userIDs          map[string]bool
	id               wire.ID
	run              uint64
	ready            map[string]bool
	poisoned, closed bool
	existing         bool
	// Fault hooks are package-private; tests simulate failure before commit
	// and a lost commit acknowledgement. Production cannot disable fsync.
	beforeCommit, afterCommit func() error
}

func key(n uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, n)
	return b
}
func number(b *bolt.Bucket, name string) (uint64, error) {
	raw := b.Get([]byte(name))
	if len(raw) != 8 {
		return 0, ErrCorrupt
	}
	return binary.BigEndian.Uint64(raw), nil
}
func putNumber(b *bolt.Bucket, name string, n uint64) error { return b.Put([]byte(name), key(n)) }
func putJSON(b *bolt.Bucket, id uint64, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(raw) > maxValue {
		return ErrLimit
	}
	return b.Put(key(id), raw)
}
func ownerKey(account, user string) string { return account + "\x00" + user }
func accountKey(account string) string     { return account + "\x00" }
func (s *Store) owner(account, user string) (Account, User, error) {
	a, ok := s.accounts[account]
	if !ok {
		return Account{}, User{}, ErrScope
	}
	u, ok := s.users[ownerKey(account, user)]
	if !ok {
		return Account{}, User{}, ErrScope
	}
	return a, u, nil
}
func (s *Store) ownsFolder(scope Scope, folder string) bool {
	_, u, err := s.owner(scope.Account, scope.User)
	if err != nil {
		return false
	}
	for _, f := range u.Folders {
		if f == folder {
			return true
		}
	}
	return false
}

// Open requires an existing private directory on a local filesystem. The bbolt
// exclusive file lock prevents a second process from owning this store. Every
// reopen advances Run and resets all admission gates; old grants remain held.
func Open(path string, config Config, lockTimeout time.Duration) (*Store, error) {
	return OpenCompatible(path, config, nil, lockTimeout)
}

// OpenCompatible accepts only exact caller-supplied profiles under the same
// exclusive DB lock used by Open. Existing limits are retained, never inferred
// from arbitrary saved configuration and never raised by reopening. Open with
// no alternatives remains strict. Fresh stores use config.
func OpenCompatible(path string, config Config, alternatives []Config, lockTimeout time.Duration) (*Store, error) {
	if lockTimeout <= 0 || lockTimeout > 30*time.Second {
		return nil, ErrConfig
	}
	c, configBytes, err := canonicalConfig(config)
	if err != nil {
		return nil, err
	}
	profiles := []Config{c}
	profileBytes := [][]byte{configBytes}
	if len(alternatives) > 16 {
		return nil, ErrConfig
	}
	for _, alternative := range alternatives {
		ac, raw, e := canonicalConfig(alternative)
		if e != nil || !sameOwners(c, ac) && !sameDataFolderOwners(c, ac) && !sameDataFolderOwners(ac, c) {
			return nil, ErrConfig
		}
		profiles = append(profiles, ac)
		profileBytes = append(profileBytes, raw)
	}
	if info, e := os.Lstat(path); e == nil {
		if !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
			return nil, ErrConfig
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: lockTimeout, NoSync: false, NoGrowSync: false})
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, config: c, ready: map[string]bool{}, accounts: map[string]Account{}, users: map[string]User{}, userIDs: map[string]bool{}}
	for _, a := range c.Accounts {
		s.accounts[a.ID] = a
		for _, u := range a.Users {
			s.users[ownerKey(a.ID, u.ID)] = u
			s.userIDs[u.ID] = true
		}
	}
	err = db.Update(func(tx *bolt.Tx) error {
		m := tx.Bucket(metaBucket)
		if m == nil {
			// Never initialize over a different database or partially defined schema.
			k, _ := tx.Cursor().First()
			if k != nil {
				return ErrCorrupt
			}
			for _, name := range [][]byte{metaBucket, grantBucket, attemptBucket, balanceBucket, countBucket} {
				if _, e := tx.CreateBucket(name); e != nil {
					return e
				}
			}
			m = tx.Bucket(metaBucket)
			if _, e := rand.Read(s.id[:]); e != nil {
				return e
			}
			if s.id == (wire.ID{}) {
				return ErrState
			}
			if e := m.Put([]byte("store"), s.id[:]); e != nil {
				return e
			}
			if e := m.Put([]byte("config"), configBytes); e != nil {
				return e
			}
			for _, name := range []string{"run", "last_grant", "last_attempt", "grant_count", "attempt_count"} {
				if e := putNumber(m, name, 0); e != nil {
					return e
				}
			}
			if e := putNumber(m, "schema", 2); e != nil {
				return e
			}
			for user := range s.userIDs {
				if e := tx.Bucket(countBucket).Put([]byte(user), make([]byte, 16)); e != nil {
					return e
				}
			}
			for _, a := range c.Accounts {
				if e := setBalance(tx, accountKey(a.ID), pool.Amount{}); e != nil {
					return e
				}
				for _, u := range a.Users {
					if e := setBalance(tx, ownerKey(a.ID, u.ID), pool.Amount{}); e != nil {
						return e
					}
				}
			}
		} else {
			s.existing = true
			v, e := number(m, "schema")
			if e != nil || (v != 1 && v != 2 && v != schema) {
				return ErrCorrupt
			}
			// Schema 1 has only local records; new fields decode as zero.
			// Audit and migration commit atomically; no budget is reset.
			if v == 1 {
				if e = putNumber(m, "schema", 2); e != nil {
					return e
				}
			}
			matched := false
			for i, raw := range profileBytes {
				if bytes.Equal(m.Get([]byte("config")), raw) {
					s.setConfig(profiles[i])
					matched = true
					break
				}
			}
			if !matched {
				return ErrConfig
			}
			raw := m.Get([]byte("store"))
			if len(raw) != 16 {
				return ErrCorrupt
			}
			copy(s.id[:], raw)
			if s.id == (wire.ID{}) {
				return ErrCorrupt
			}
			for _, name := range [][]byte{grantBucket, attemptBucket, balanceBucket, countBucket} {
				if tx.Bucket(name) == nil {
					return ErrCorrupt
				}
			}
		}
		if e := rebuildRemoteIndex(tx); e != nil {
			return e
		}
		if e := s.audit(tx); e != nil {
			return e
		}
		// A previous process cannot complete these writers. Do not retry or
		// free them: classify their possible mailbox effect conservatively.
		var uncertain []uint64
		if e := tx.Bucket(attemptBucket).ForEach(func(k, v []byte) error {
			var a Attempt
			if e := decode(v, &a); e != nil {
				return e
			}
			if a.State == Started {
				uncertain = append(uncertain, a.Ref.ID)
			}
			return nil
		}); e != nil {
			return e
		}
		for _, id := range uncertain {
			var a Attempt
			if e := decode(tx.Bucket(attemptBucket).Get(key(id)), &a); e != nil {
				return e
			}
			a.State = Unknown
			if e := putJSON(tx.Bucket(attemptBucket), id, a); e != nil {
				return e
			}
		}
		old, e := number(m, "run")
		if e != nil || old == ^uint64(0) {
			return ErrCorrupt
		}
		s.run = old + 1
		return putNumber(m, "run", s.run)
	})
	if err == nil && runtime.GOOS != "windows" {
		// Persist the directory entry too, particularly on initial creation.
		var dir *os.File
		dir, err = os.Open(filepath.Dir(path))
		if err == nil {
			err = dir.Sync()
			closeErr := dir.Close()
			if err == nil {
				err = closeErr
			}
		}
	}
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}
func expectedError(err error) bool {
	return errors.Is(err, ErrScope) || errors.Is(err, ErrLimit) || errors.Is(err, ErrState) ||
		errors.Is(err, ErrOrder) || errors.Is(err, ErrMissing) || errors.Is(err, ErrRecovery) || errors.Is(err, ErrGeneration)
}
func (s *Store) write(fn func(*bolt.Tx) error) error {
	queued := time.Now()
	s.mu.Lock()
	s.metrics.queueNS.Add(uint64(time.Since(queued)))
	defer s.mu.Unlock()
	return s.writeLocked(fn)
}
func (s *Store) writeLocked(fn func(*bolt.Tx) error) error {
	if s.closed || s.poisoned {
		return ErrPoisoned
	}
	begin := time.Now()
	err := s.db.Update(func(tx *bolt.Tx) error {
		if e := fn(tx); e != nil {
			return e
		}
		if s.beforeCommit != nil {
			return s.beforeCommit()
		}
		return nil
	})
	if err == nil && s.afterCommit != nil {
		err = s.afterCommit()
	}
	elapsed := uint64(time.Since(begin))
	s.metrics.count.Add(1)
	s.metrics.commitNS.Add(elapsed)
	for old := s.metrics.maxNS.Load(); elapsed > old; old = s.metrics.maxNS.Load() {
		if s.metrics.maxNS.CompareAndSwap(old, elapsed) {
			break
		}
	}
	if err != nil {
		s.metrics.failures.Add(1)
	}
	if err != nil && !expectedError(err) {
		s.poisoned = true
	}
	return err
}
func (s *Store) read(fn func(*bolt.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.poisoned {
		return ErrPoisoned
	}
	err := s.db.View(fn)
	if err != nil && !expectedError(err) {
		s.poisoned = true
	}
	return err
}
func (s *Store) Run() uint64 { return s.run }
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.db.Close()
}

// EnableAfterInventory is a trusted local scanner completion, NOT a peer API.
// The owner must first finish a bounded IMAP inventory/reconciliation for this
// account/user. Even after enabling, unresolved debt continues to occupy quota.
func (s *Store) EnableAfterInventory(account, user string, run uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.poisoned {
		return ErrPoisoned
	}
	if run != s.run {
		return ErrGeneration
	}
	if _, _, err := s.owner(account, user); err != nil {
		return err
	}
	s.ready[ownerKey(account, user)] = true
	return nil
}
func balance(tx *bolt.Tx, owner string) (pool.Amount, error) {
	raw := tx.Bucket(balanceBucket).Get([]byte(owner))
	if len(raw) != 16 {
		return pool.Amount{}, ErrCorrupt
	}
	return pool.Amount{Bytes: binary.BigEndian.Uint64(raw), Messages: binary.BigEndian.Uint64(raw[8:])}, nil
}
func setBalance(tx *bolt.Tx, owner string, n pool.Amount) error {
	raw := append(key(n.Bytes), key(n.Messages)...)
	return tx.Bucket(balanceBucket).Put([]byte(owner), raw)
}
func changeBalance(tx *bolt.Tx, scope Scope, delta pool.Amount, release bool) error {
	for _, k := range []string{accountKey(scope.Account), ownerKey(scope.Account, scope.User)} {
		n, err := balance(tx, k)
		if err != nil {
			return err
		}
		if release {
			if !within(delta, n) {
				return ErrCorrupt
			}
			n = sub(n, delta)
		} else {
			n = add(n, delta)
		}
		if err = setBalance(tx, k, n); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) Held(account, user string) (pool.Amount, error) {
	var n pool.Amount
	err := s.read(func(tx *bolt.Tx) error {
		if _, ok := s.accounts[account]; !ok {
			return ErrScope
		}
		if user != "" {
			if _, _, e := s.owner(account, user); e != nil {
				return e
			}
		}
		var e error
		n, e = balance(tx, ownerKey(account, user))
		return e
	})
	if err != nil {
		return pool.Amount{}, err
	}
	return n, nil
}

// Metadata shares are protected per user across ALL accounts. They cannot be
// consumed by another user's tiny grants or repeated physical attempts.
func (s *Store) userCount(tx *bolt.Tx, user string, grant, increase bool) error {
	b := tx.Bucket(countBucket)
	raw := b.Get([]byte(user))
	if len(raw) != 16 {
		return ErrCorrupt
	}
	copyValue := append([]byte(nil), raw...)
	offset, maximum := 8, s.config.MaxAttempts/len(s.userIDs)
	if grant {
		offset, maximum = 0, s.config.MaxGrants/len(s.userIDs)
	}
	n := binary.BigEndian.Uint64(copyValue[offset:])
	if increase {
		if n >= uint64(maximum) {
			return ErrLimit
		}
		n++
	} else {
		if n == 0 {
			return ErrCorrupt
		}
		n--
	}
	binary.BigEndian.PutUint64(copyValue[offset:], n)
	return b.Put([]byte(user), copyValue)
}

// StoreID is the persistent, public ledger identity. A network peer must learn
// it through authenticated startup; the value alone does not authorize grants.
func (s *Store) StoreID() wire.ID { return s.id }

// HeldByOrigin separates admission scheduling shares without changing durable
// balances. Closed, unknown and prior-run grants retain their actual held debt.
func (s *Store) HeldByOrigin(account, user string) (local, remote pool.Amount, err error) {
	err = s.read(func(tx *bolt.Tx) error {
		if _, _, e := s.owner(account, user); e != nil {
			return e
		}
		count := 0
		return tx.Bucket(grantBucket).ForEach(func(k, v []byte) error {
			count++
			if count > s.config.MaxGrants {
				return ErrCorrupt
			}
			var g Grant
			if e := decode(v, &g); e != nil {
				return e
			}
			if g.Ref.Scope.Account != account || g.Ref.Scope.User != user {
				return nil
			}
			if g.Remote {
				remote = add(remote, held(g))
			} else {
				local = add(local, held(g))
			}
			return nil
		})
	})
	if err != nil {
		return pool.Amount{}, pool.Amount{}, err
	}
	return
}
