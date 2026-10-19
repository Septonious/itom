// Package journal owns one durable budget for local writers and remote grants.
// It stores no user payload or session keys. Its methods block on disk and must
// run on a metadata worker, never on the session actor's event loop.
package journal

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"itom.local/core/pool"
	"itom.local/core/wire"
)

var (
	ErrConfig     = errors.New("JOURNAL_CONFIG")
	ErrScope      = errors.New("JOURNAL_SCOPE")
	ErrLimit      = errors.New("JOURNAL_LIMIT")
	ErrState      = errors.New("JOURNAL_STATE")
	ErrOrder      = errors.New("JOURNAL_REMOTE_COUNTER")
	ErrMissing    = errors.New("JOURNAL_RETIRED_OR_UNKNOWN")
	ErrRecovery   = errors.New("JOURNAL_INVENTORY_REQUIRED")
	ErrGeneration = errors.New("JOURNAL_GENERATION")
	ErrPoisoned   = errors.New("JOURNAL_IO_UNCERTAIN")
	ErrCorrupt    = errors.New("JOURNAL_CORRUPT")
)

const (
	schema            = 3
	MaxDecoded        = 1 << 20
	MaxEncoded        = 2 << 20
	maxValue          = 4096
	MaxFoldersPerUser = 10 // two legacy/control folders plus four DATA shards each way
)

type User struct {
	ID      string
	Limit   pool.Amount
	Folders []string
}
type Account struct {
	ID, Provider string
	Limit        pool.Amount
	Users        []User
}
type Config struct {
	Accounts               []Account
	MaxGrants, MaxAttempts int
}
type Scope struct {
	Account, User string
	Session       wire.ID
}

// Store prevents stale handles from aliasing IDs after a database replacement.
type GrantRef struct {
	Store wire.ID
	ID    uint64
	Scope Scope
}
type Grant struct {
	Ref                   GrantRef
	Run                   uint64
	Limit, Used, Released pool.Amount
	Closed                bool
	Remote                bool
	// Registered is a contiguous remote descriptor prefix, including cleaned rows.
	Registered pool.Amount
}
type AttemptRef struct {
	Grant GrantRef
	ID    uint64
}
type State uint8

const (
	Prepared State = iota
	Started
	Unknown
	Committed
)

type Location struct {
	UIDValidity, UID uint32
}
type Attempt struct {
	Ref                        AttemptRef
	Run                        uint64
	Batch                      wire.ID
	Folder                     string
	EncodedBytes, DecodedBytes uint64
	Digest                     [32]byte
	State                      State
	// Ordinal is zero for local attempts; remote ordinals never repeat per grant.
	Ordinal  uint64
	Location Location
}

// Observation must come from a full, bounded FETCH in the configured folder.
// Markers and Message-ID alone are never evidence of ownership.
type Observation struct {
	Account, Folder string
	// Required for remote attempts: actual stored MIME size from the scanner.
	EncodedBytes uint64
	Location     Location
	Decoded      []byte
}

// Proof batches remain bounded independently of startup inventory size.
const MaxProofBatch = 32

type ObservedAttempt struct {
	Ref         AttemptRef
	Observation Observation
}

// Deletion is a trusted worker's same-UIDVALIDITY absence/deletion proof.
type Deletion struct {
	Ref      AttemptRef
	Location Location
}

func within(a, b pool.Amount) bool { return a.Bytes <= b.Bytes && a.Messages <= b.Messages }
func add(a, b pool.Amount) pool.Amount {
	return pool.Amount{Bytes: a.Bytes + b.Bytes, Messages: a.Messages + b.Messages}
}
func sub(a, b pool.Amount) pool.Amount {
	return pool.Amount{Bytes: a.Bytes - b.Bytes, Messages: a.Messages - b.Messages}
}
func charge(a Attempt) pool.Amount { return pool.Amount{Bytes: a.EncodedBytes, Messages: 1} }
func held(g Grant) pool.Amount {
	n := g.Limit
	if g.Closed {
		n = g.Used
	}
	return sub(n, g.Released)
}
func validAmount(n pool.Amount) bool {
	return n.Bytes > 0 && n.Bytes <= 1<<40 && n.Messages > 0 && n.Messages <= 1<<24
}
func validID(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for _, c := range []byte(s) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}
func validUser(s string) bool {
	if len(s) != 8 || s[0] == '0' {
		return false
	}
	for _, c := range []byte(s) {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
func validFolder(s string) bool {
	if len(s) == 0 || len(s) > 255 || !utf8.ValidString(s) || strings.EqualFold(s, "INBOX") {
		return false
	}
	for _, c := range s {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}
func canonicalConfig(c Config) (Config, []byte, error) {
	if len(c.Accounts) == 0 || len(c.Accounts) > 64 || c.MaxGrants < 1 || c.MaxGrants > 65536 || c.MaxAttempts < 1 || c.MaxAttempts > 65536 {
		return Config{}, nil, ErrConfig
	}
	out := Config{MaxGrants: c.MaxGrants, MaxAttempts: c.MaxAttempts}
	seen := map[string]bool{}
	allUsers := map[string]bool{}
	provider := c.Accounts[0].Provider
	if len(provider) == 0 || len(provider) > 255 || !utf8.ValidString(provider) {
		return Config{}, nil, ErrConfig
	}
	for _, a := range c.Accounts {
		if !validID(a.ID) || seen[a.ID] || a.Provider != provider || !validAmount(a.Limit) || len(a.Users) == 0 || len(a.Users) > 1024 {
			return Config{}, nil, ErrConfig
		}
		seen[a.ID] = true
		copyAccount := Account{ID: a.ID, Provider: a.Provider, Limit: a.Limit}
		users, folders := map[string]bool{}, map[string]bool{}
		total := pool.Amount{}
		for _, u := range a.Users {
			if !validUser(u.ID) || users[u.ID] || !validAmount(u.Limit) || !within(u.Limit, a.Limit) || len(u.Folders) == 0 || len(u.Folders) > MaxFoldersPerUser {
				return Config{}, nil, ErrConfig
			}
			users[u.ID] = true
			allUsers[u.ID] = true
			total = add(total, u.Limit)
			copyUser := User{ID: u.ID, Limit: u.Limit, Folders: append([]string(nil), u.Folders...)}
			for _, f := range u.Folders {
				if !validFolder(f) || folders[f] {
					return Config{}, nil, ErrConfig
				}
				folders[f] = true
			}
			sort.Strings(copyUser.Folders)
			copyAccount.Users = append(copyAccount.Users, copyUser)
		}
		if !within(total, a.Limit) {
			return Config{}, nil, ErrConfig
		}
		sort.Slice(copyAccount.Users, func(i, j int) bool { return copyAccount.Users[i].ID < copyAccount.Users[j].ID })
		out.Accounts = append(out.Accounts, copyAccount)
	}
	sort.Slice(out.Accounts, func(i, j int) bool { return out.Accounts[i].ID < out.Accounts[j].ID })
	if c.MaxGrants < len(allUsers) || c.MaxAttempts < len(allUsers) {
		return Config{}, nil, ErrConfig
	}
	raw, err := json.Marshal(out)
	if err != nil || len(raw) > 1<<20 {
		return Config{}, nil, ErrConfig
	}
	return out, raw, nil
}
func decode(raw []byte, value any) error {
	if len(raw) == 0 || len(raw) > maxValue {
		return ErrCorrupt
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return ErrCorrupt
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return ErrCorrupt
	}
	return nil
}
