package servercfg

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	bolt "go.etcd.io/bbolt"
	"io"
	"itom.local/core/mailbox"
	"itom.local/core/wire"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var ErrConfig = errors.New("SERVER_CONFIG")
var ErrConfigSize = errors.New("SERVER_CONFIG_TOO_LARGE")

// Limit decoded configuration bytes, not the number of carrier accounts.
// All writers and readers share this bound so a valid update remains readable.
const MaxConfigBytes = 1 << 20

type Account struct {
	ID   string         `json:"id"`
	Mail mailbox.Config `json:"imap"`
}
type User struct {
	ID      string `json:"uid"`
	Name    string `json:"name"`
	Key     string `json:"key"`
	Up      string `json:"up"`
	Down    string `json:"down"`
	Enabled bool   `json:"enabled"`
}

// Removed identities fence old invitations and retain the location of physical
// journals. Removal never deletes those journals or refunds mailbox debt.
type UserIdentity struct {
	ID   string `json:"uid"`
	Up   string `json:"up"`
	Down string `json:"down"`
}
type Config struct {
	Transport       Transport         `json:"transport,omitempty"`
	Notice          wire.Notice       `json:"server_message,omitempty"`
	DiagnosticsMode string            `json:"diagnostics_mode,omitempty"`
	Schema          int               `json:"schema"`
	Server          wire.ID           `json:"server"`
	Provider        string            `json:"provider"`
	Accounts        []Account         `json:"accounts"`
	Retired         []AccountIdentity `json:"retired_accounts,omitempty"`
	Users           []User            `json:"users"`
	RetiredUsers    []UserIdentity    `json:"retired_users,omitempty"`
}
type Profile struct {
	Transport Transport `json:"transport,omitempty"`
	Schema    int       `json:"schema"`
	Kind      string    `json:"kind"`
	Server    wire.ID   `json:"server"`
	Provider  string    `json:"provider"`
	User      User      `json:"user"`
	Accounts  []Account `json:"accounts"`
}

func clean(s string, n int) bool {
	return len(s) > 0 && len(s) <= n && !strings.ContainsAny(s, "\r\n\x00\x1b")
}
func accountID(s string) bool {
	if len(s) == 0 || len(s) > 40 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
func (c Config) Validate() error {
	if err := c.Transport.Validate(); err != nil {
		return err
	}
	if !wire.ValidNoticeText(c.Notice.Text) || c.Notice.Revision > wire.MaxOffset || c.Notice.Revision == 0 && c.Notice.Text != "" || c.Notice.Revision > 0 && c.Schema < 4 {
		return ErrConfig
	}
	if c.DiagnosticsMode != "" && c.DiagnosticsMode != "on" && c.DiagnosticsMode != "off" {
		return ErrConfig
	}
	if (c.Schema < 1 || c.Schema > 4) || c.Schema == 1 && len(c.Retired) != 0 || c.Schema < 3 && len(c.RetiredUsers) != 0 || c.Server == (wire.ID{}) || !clean(c.Provider, 80) || len(c.Users) > 2 || len(c.Retired) > 1024 || len(c.RetiredUsers) > 1024 {
		return ErrConfig
	}
	seen := map[string]bool{}
	logins := map[string]bool{}
	host := ""
	for _, a := range c.Accounts {
		if !accountID(a.ID) || seen[a.ID] || a.Mail.Validate() != nil {
			return ErrConfig
		}
		seen[a.ID] = true
		if host == "" {
			host = strings.ToLower(a.Mail.Host)
		}
		if strings.ToLower(a.Mail.Host) != host || logins[strings.ToLower(a.Mail.Username)] {
			return ErrConfig
		}
		logins[strings.ToLower(a.Mail.Username)] = true
	}
	retiredIdentities := map[string]bool{}
	for _, a := range c.Retired {
		if !accountID(a.ID) || seen[a.ID] || !clean(a.Host, 255) || !clean(a.Username, 320) {
			return ErrConfig
		}
		seen[a.ID] = true
		k := strings.ToLower(a.Host) + "\x00" + strings.ToLower(a.Username)
		if retiredIdentities[k] {
			return ErrConfig
		}
		retiredIdentities[k] = true
		for _, active := range c.Accounts {
			if sameMailbox(a, active.Mail) {
				return ErrConfig
			}
		}
	}
	ids := map[string]bool{}
	folders := map[string]bool{}
	for _, u := range c.Users {
		if len(u.ID) != 8 || u.ID[0] == '0' || ids[u.ID] || !clean(u.Name, 80) {
			return ErrConfig
		}
		for _, x := range u.ID {
			if x < '0' || x > '9' {
				return ErrConfig
			}
		}
		key, e := hex.DecodeString(u.Key)
		if e != nil || len(key) != 32 {
			return ErrConfig
		}
		ids[u.ID] = true
		for _, f := range []string{u.Up, u.Down} {
			if !strings.HasPrefix(f, "itom-") || !clean(f, 180) || folders[f] {
				return ErrConfig
			}
			folders[f] = true
		}
	}
	for _, u := range c.RetiredUsers {
		if len(u.ID) != 8 || u.ID[0] == '0' || ids[u.ID] {
			return ErrConfig
		}
		for _, x := range u.ID {
			if x < '0' || x > '9' {
				return ErrConfig
			}
		}
		ids[u.ID] = true
		for _, f := range []string{u.Up, u.Down} {
			if !strings.HasPrefix(f, "itom-") || !clean(f, 180) || folders[f] {
				return ErrConfig
			}
			folders[f] = true
		}
	}
	if err := c.TransportForUsers().Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return ErrConfig
	}
	if len(raw) > MaxConfigBytes {
		return ErrConfigSize
	}
	return nil
}
func New(provider string) (Config, error) {
	c := Config{Schema: 1, Provider: provider}
	_, e := rand.Read(c.Server[:])
	if e != nil {
		return c, e
	}
	return c, c.Validate()
}
func (c *Config) AddUser(name string) (User, error) {
	if len(c.Users) >= 2 {
		return User{}, ErrConfig
	}
	var u User
	for {
		n, e := rand.Int(rand.Reader, big.NewInt(90000000))
		if e != nil {
			return u, e
		}
		u.ID = fmt.Sprint(n.Int64() + 10000000)
		used := false
		for _, x := range c.Users {
			if x.ID == u.ID {
				used = true
			}
		}
		for _, x := range c.RetiredUsers {
			if x.ID == u.ID {
				used = true
			}
		}
		if !used {
			break
		}
	}
	key := make([]byte, 32)
	salt := make([]byte, 8)
	if _, e := rand.Read(key); e != nil {
		return u, e
	}
	if _, e := rand.Read(salt); e != nil {
		return u, e
	}
	prefix := "itom-" + hex.EncodeToString(c.Server[:4]) + "-" + u.ID + "-" + hex.EncodeToString(salt)
	u.Name = name
	u.Key = hex.EncodeToString(key)
	u.Up = prefix + "-up"
	u.Down = prefix + "-down"
	u.Enabled = true
	c.Users = append(c.Users, u)
	return u, c.Validate()
}
func private(path string) error {
	if s, e := os.Lstat(path); e == nil {
		if !s.Mode().IsRegular() || runtime.GOOS != "windows" && s.Mode().Perm()&0077 != 0 {
			return ErrConfig
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	return nil
}
func Update(path string, fn func(*Config) error) error {
	if e := private(path); e != nil {
		return e
	}
	db, e := bolt.Open(path, 0600, &bolt.Options{Timeout: time.Second})
	if e != nil {
		return e
	}
	e = db.Update(func(tx *bolt.Tx) error {
		b, e := tx.CreateBucketIfNotExists([]byte("config"))
		if e != nil {
			return e
		}
		var c Config
		if raw := b.Get([]byte("json")); raw != nil {
			if len(raw) > MaxConfigBytes || json.Unmarshal(raw, &c) != nil || c.Validate() != nil {
				return ErrConfig
			}
		}
		if e = fn(&c); e != nil {
			return e
		}
		if e = c.Validate(); e != nil {
			return e
		}
		raw, e := json.Marshal(c)
		if e != nil {
			return e
		}
		return b.Put([]byte("json"), raw)
	})
	closeErr := db.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	if runtime.GOOS != "windows" {
		dir, err := os.Open(filepath.Dir(path))
		if err != nil {
			return err
		}
		defer dir.Close()
		return dir.Sync()
	}
	return nil
}

func Load(path string) (Config, error) {
	var c Config
	if e := private(path); e != nil {
		return c, e
	}
	if _, e := os.Stat(path); e != nil {
		return c, e
	}
	db, e := bolt.Open(path, 0600, &bolt.Options{ReadOnly: true, Timeout: time.Second})
	if e != nil {
		return c, e
	}
	defer db.Close()
	e = db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("config"))
		if b == nil {
			return ErrConfig
		}
		raw := b.Get([]byte("json"))
		if len(raw) > MaxConfigBytes || json.Unmarshal(raw, &c) != nil {
			return ErrConfig
		}
		return c.Validate()
	})
	return c, e
}
func (c Config) Export(uid, path string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if len(c.Accounts) == 0 {
		return ErrConfig
	}
	var u User
	for _, x := range c.Users {
		if x.ID == uid && x.Enabled {
			u = x
		}
	}
	if u.ID == "" {
		return ErrConfig
	}
	p := Profile{Schema: 1, Kind: "itom-imap-probe", Server: c.Server, Provider: c.Provider, User: u, Accounts: c.Accounts, Transport: c.TransportForUsers()}
	b, e := json.MarshalIndent(p, "", "  ")
	if e != nil {
		return e
	}
	if len(b) > MaxConfigBytes {
		return ErrConfigSize
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		os.Remove(path)
		return e
	}
	return ce
}
func ReadProfile(path string) (Profile, error) {
	var p Profile
	f, e := os.Open(path)
	if e != nil {
		return p, e
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, MaxConfigBytes+1))
	if e != nil {
		return p, e
	}
	if len(raw) > MaxConfigBytes {
		return p, ErrConfigSize
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e = d.Decode(&p); e != nil {
		return p, ErrConfig
	}
	if d.Decode(new(any)) != io.EOF {
		return p, ErrConfig
	}
	if p.Schema != 1 || p.Kind != "itom-imap-probe" || !p.User.Enabled {
		return p, ErrConfig
	}
	c := Config{Schema: 1, Server: p.Server, Provider: p.Provider, Users: []User{p.User}, Accounts: p.Accounts, Transport: p.Transport}
	if len(p.Accounts) == 0 {
		return p, ErrConfig
	}
	return p, c.Validate()
}
