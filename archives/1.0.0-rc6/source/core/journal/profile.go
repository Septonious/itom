package journal

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"strconv"
	"strings"

	bolt "go.etcd.io/bbolt"
	"itom.local/core/pool"
)

// sameOwners permits only capacity changes, never a new owner/provider/folder.
func sameOwners(a, b Config) bool {
	if a.MaxGrants != b.MaxGrants || a.MaxAttempts != b.MaxAttempts || len(a.Accounts) != len(b.Accounts) {
		return false
	}
	for i, x := range a.Accounts {
		y := b.Accounts[i]
		if x.ID != y.ID || x.Provider != y.Provider || len(x.Users) != len(y.Users) {
			return false
		}
		for j, u := range x.Users {
			v := y.Users[j]
			if u.ID != v.ID || len(u.Folders) != len(v.Folders) {
				return false
			}
			for k, f := range u.Folders {
				if f != v.Folders[k] {
					return false
				}
			}
		}
	}
	return true
}

func (s *Store) setConfig(c Config) {
	s.config = c
	s.accounts = map[string]Account{}
	s.users = map[string]User{}
	s.userIDs = map[string]bool{}
	for _, a := range c.Accounts {
		s.accounts[a.ID] = a
		for _, u := range a.Users {
			s.users[ownerKey(a.ID, u.ID)] = u
			s.userIDs[u.ID] = true
		}
	}
}

// Config returns a detached, canonical snapshot of the effective profile.
func (s *Store) Config() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, _, _ := canonicalConfig(s.config)
	return c
}

func (s *Store) Existing() bool { return s.existing }

func (s *Store) Limit(account, user string) (pool.Amount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.poisoned {
		return pool.Amount{}, ErrPoisoned
	}
	_, u, err := s.owner(account, user)
	return u.Limit, err
}

// ProfileMigration is the last atomic profile change, with a monotonic sequence.
// It contains configuration hashes and the audited debt, never payload or keys.
// Earlier outcomes need no growing event table: the current config and this
// bounded record commit in the same transaction.
type ProfileMigration struct {
	Version          uint8
	Sequence, Run    uint64
	From, To         [32]byte
	Grants, Attempts uint64
	Outcome          string
}

func (s *Store) LastMigration() (ProfileMigration, error) {
	var result ProfileMigration
	err := s.read(func(tx *bolt.Tx) error {
		raw := tx.Bucket(metaBucket).Get([]byte("profile_migration"))
		if raw == nil {
			return nil
		}
		return decode(raw, &result)
	})
	return result, err
}

// MigrateConfig changes only limits under the exclusive DB lock. Audit runs
// against BOTH profiles, so downgrade cannot forget debt or oversized grants.
// Call only on an offline store (before enabling admission/starting writers).
// Unknown APPEND debt remains charged. Lost commit acknowledgement poisons the
// handle; reopen with an explicit allowlist to discover the committed profile.
func (s *Store) MigrateConfig(next Config) error {
	return s.migrateConfig(next, sameOwners)
}

// ExtendDataFolders is an offline, additive, capacity-neutral change. It keeps
// legacy folders and every uncertain attempt; new shards share existing debt.
func (s *Store) ExtendDataFolders(next Config) error {
	return s.migrateConfig(next, func(a, b Config) bool {
		if !sameDataFolderOwners(a, b) {
			return false
		}
		for i, x := range a.Accounts {
			if x.Limit != b.Accounts[i].Limit {
				return false
			}
			for j, u := range x.Users {
				if u.Limit != b.Accounts[i].Users[j].Limit {
					return false
				}
			}
		}
		return true
	})
}

func dataShardFolder(folder string) bool {
	if len(folder) < 8 || folder[len(folder)-8:len(folder)-2] != "-data-" {
		return false
	}
	n, err := strconv.Atoi(folder[len(folder)-2:])
	return err == nil && n >= 0 && n < 4 && folder[len(folder)-2] == '0'
}

func sameDataFolderOwners(a, b Config) bool {
	if a.MaxGrants != b.MaxGrants || a.MaxAttempts != b.MaxAttempts || len(a.Accounts) != len(b.Accounts) {
		return false
	}
	for i, x := range a.Accounts {
		y := b.Accounts[i]
		if x.ID != y.ID || x.Provider != y.Provider || len(x.Users) != len(y.Users) {
			return false
		}
		for j, u := range x.Users {
			v := y.Users[j]
			if u.ID != v.ID {
				return false
			}
			old := map[string]bool{}
			next := map[string]bool{}
			for _, f := range u.Folders {
				old[f] = true
			}
			for _, f := range v.Folders {
				next[f] = true
			}
			for f := range old {
				if !next[f] {
					return false
				}
			}
			for f := range next {
				if old[f] {
					continue
				}
				allowed := false
				for base := range old {
					if !dataShardFolder(base) && dataShardFolder(f) && strings.TrimSuffix(f, f[len(f)-8:]) == base {
						allowed = true
						break
					}
				}
				if !allowed {
					return false
				}
			}
		}
	}
	return true
}

func (s *Store) migrateConfig(next Config, compatible func(Config, Config) bool) error {
	c, raw, err := canonicalConfig(next)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err = s.writeLocked(func(tx *bolt.Tx) error {
		if len(s.ready) != 0 {
			return ErrState
		}
		if !compatible(s.config, c) {
			return ErrScope
		}
		if e := s.audit(tx); e != nil {
			return e
		}
		m := tx.Bucket(metaBucket)
		before := m.Get([]byte("config"))
		if bytes.Equal(before, raw) {
			return nil
		}
		candidate := &Store{id: s.id}
		candidate.setConfig(c)
		if e := candidate.audit(tx); e != nil {
			// The existing records already passed audit; the proposed profile cannot
			// contain them. This is a denied capacity transition, not a corrupt store.
			return ErrLimit
		}
		var record ProfileMigration
		if previous := m.Get([]byte("profile_migration")); previous != nil {
			if e := decode(previous, &record); e != nil {
				return e
			}
		}
		if record.Sequence == ^uint64(0) {
			return ErrCorrupt
		}
		record.Sequence++
		record.Version = 1
		record.Run = s.run
		record.From, record.To = sha256.Sum256(before), sha256.Sum256(raw)
		record.Grants, err = number(m, "grant_count")
		if err != nil {
			return err
		}
		record.Attempts, err = number(m, "attempt_count")
		if err != nil {
			return err
		}
		record.Outcome = "audited"
		encoded, e := json.Marshal(record)
		if e != nil {
			return e
		}
		if e = m.Put([]byte("profile_migration"), encoded); e != nil {
			return e
		}
		return m.Put([]byte("config"), raw)
	})
	if err == nil {
		// No admission is enabled during an offline migration. Config is published
		// only after the durable transaction acknowledges success.
		s.setConfig(c)
	}
	return err
}

func auditProfileMigration(tx *bolt.Tx) error {
	m := tx.Bucket(metaBucket)
	raw := m.Get([]byte("profile_migration"))
	if raw == nil {
		return nil
	}
	var record ProfileMigration
	if err := decode(raw, &record); err != nil {
		return err
	}
	run, err := number(m, "run")
	if err != nil {
		return err
	}
	if record.Version != 1 || record.Sequence == 0 || record.Run == 0 || record.Run > run || record.Outcome != "audited" || record.From == ([32]byte{}) || record.To != sha256.Sum256(m.Get([]byte("config"))) {
		return ErrCorrupt
	}
	return nil
}
