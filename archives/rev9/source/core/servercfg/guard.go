package servercfg

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"itom.local/core/wire"
	"time"

	bolt "go.etcd.io/bbolt"
)

var ErrGuardPending = errors.New("ADMIN_CHANGE_PENDING")
var ErrGuardConflict = errors.New("ADMIN_CHANGE_CONFLICT")

const GuardLossTimeout = 60 * time.Second
const GuardHealthyTrial = 60 * time.Second
const GuardMaximumTrial = 15 * time.Minute

// Guard is durable before the independent watchdog is started. Before and Plan
// contain secrets and must never enter a public status response or a log.
type Guard struct {
	ID             string          `json:"id"`
	Owner          string          `json:"owner"`
	State          string          `json:"state"`
	ActionDoneMS   int64           `json:"action_done_ms"`
	PlanHash       string          `json:"plan_hash"`
	CreatedMS      int64           `json:"created_ms"`
	AppliedMS      int64           `json:"applied_ms"`
	LastBeatMS     int64           `json:"last_beat_ms"`
	HealthySinceMS int64           `json:"healthy_since_ms"`
	Challenge      string          `json:"challenge"`
	Plan           json.RawMessage `json:"plan,omitempty"`
	Before         json.RawMessage `json:"before,omitempty"`
	Unit           *GuardUnit      `json:"unit,omitempty"`
}

type GuardUnit struct {
	Symlink        bool   `json:"symlink"`
	Link           string `json:"link,omitempty"`
	Data           []byte `json:"data,omitempty"`
	Exists         bool   `json:"exists"`
	BootLink       string `json:"boot_link,omitempty"`
	BootLinkExists bool   `json:"boot_link_exists"`
}

func (g Guard) Active() bool {
	return g.State == "armed" || g.State == "observing" || g.State == "rolling_back"
}
func (g Guard) Expired(now time.Time) bool {
	if !g.Active() {
		return false
	}
	stamp := g.LastBeatMS
	if stamp == 0 {
		stamp = g.CreatedMS
	}
	return now.UnixMilli()-stamp >= GuardLossTimeout.Milliseconds() || now.UnixMilli()-g.CreatedMS >= GuardMaximumTrial.Milliseconds() || stamp > now.Add(5*time.Second).UnixMilli()
}
func guardNonce() (string, error) {
	var b [16]byte
	_, e := rand.Read(b[:])
	return hex.EncodeToString(b[:]), e
}
func validGuardID(id string) bool { b, e := hex.DecodeString(id); return e == nil && len(b) == 16 }

// Config and guard data share one bolt transaction, so no crash can publish a
// configuration without its rollback snapshot and phase transition.
func guardDB(path string, fn func(*bolt.Tx) error, write bool) error {
	if err := private(path); err != nil {
		return err
	}
	db, err := bolt.Open(path, 0600, &bolt.Options{ReadOnly: !write, Timeout: time.Second})
	if err != nil {
		return err
	}
	if write {
		err = db.Update(fn)
	} else {
		err = db.View(fn)
	}
	ce := db.Close()
	if err != nil {
		return err
	}
	return ce
}
func readGuard(tx *bolt.Tx, id string) (Guard, error) {
	var g Guard
	b := tx.Bucket([]byte("admin_guard"))
	if b == nil {
		return g, nil
	}
	if id == "" {
		id = string(b.Get([]byte("active")))
		if id == "" {
			id = string(b.Get([]byte("latest")))
		}
	}
	raw := b.Get([]byte("txn/" + id))
	if len(raw) == 0 {
		return g, nil
	}
	if json.Unmarshal(raw, &g) != nil || !validGuardID(g.ID) {
		return Guard{}, ErrGuardConflict
	}
	return g, nil
}
func putGuard(tx *bolt.Tx, g Guard) error {
	b, e := tx.CreateBucketIfNotExists([]byte("admin_guard"))
	if e != nil {
		return e
	}
	raw, e := json.Marshal(g)
	if e != nil {
		return e
	}
	if e = b.Put([]byte("txn/"+g.ID), raw); e != nil {
		return e
	}
	if e = b.Put([]byte("latest"), []byte(g.ID)); e != nil {
		return e
	}
	if g.Active() {
		return b.Put([]byte("active"), []byte(g.ID))
	}
	if string(b.Get([]byte("active"))) == g.ID {
		return b.Delete([]byte("active"))
	}
	return nil
}
func GuardStatus(path string) (Guard, error) {
	var g Guard
	e := guardDB(path, func(tx *bolt.Tx) error { var e error; g, e = readGuard(tx, ""); return e }, false)
	return g, e
}

// BeginGuard is idempotent for the same ID, owner and byte-identical plan. An
// older completed request cannot execute again after reconnect or HTTP retry.
func BeginGuard(path string, actor User, id string, plan []byte, now time.Time, checks ...func(Config) error) (Guard, bool, error) {
	owner := actor.ID
	var g Guard
	fresh := false
	if !validGuardID(id) || len(plan) == 0 || len(plan) > 16384 {
		return g, false, ErrGuardConflict
	}
	e := guardDB(path, func(tx *bolt.Tx) error {
		prior, e := readGuard(tx, id)
		if e != nil {
			return e
		}
		if prior.ID != "" {
			if prior.Owner != owner || prior.PlanHash != guardPlanHash(plan) {
				return ErrGuardConflict
			}
			g = prior
			return nil
		}
		active, e := readGuard(tx, "")
		if e != nil {
			return e
		}
		if active.Active() {
			return ErrGuardPending
		}
		b := tx.Bucket([]byte("config"))
		if b == nil {
			return ErrConfig
		}
		raw := b.Get([]byte("json"))
		var c Config
		if json.Unmarshal(raw, &c) != nil || c.Validate() != nil {
			return ErrConfig
		}
		ok := false
		for _, u := range c.Users {
			ok = ok || (u.ID == owner && u.Name == "admin" && u.Enabled && subtle.ConstantTimeCompare([]byte(u.Key), []byte(actor.Key)) == 1)
		}
		if !ok {
			return ErrGuardConflict
		}
		for _, check := range checks {
			if e = check(c); e != nil {
				return e
			}
		}
		challenge, e := guardNonce()
		if e != nil {
			return e
		}
		g = Guard{ID: id, Owner: owner, State: "armed", CreatedMS: now.UnixMilli(), LastBeatMS: now.UnixMilli(), Challenge: challenge, PlanHash: guardPlanHash(plan), Before: append([]byte(nil), raw...), Plan: append([]byte(nil), plan...)}
		if e = putGuard(tx, g); e != nil {
			return e
		}
		fresh = true
		return nil
	}, true)
	return g, fresh, e
}
func CancelGuard(path, id string) error {
	return guardDB(path, func(tx *bolt.Tx) error {
		g, e := readGuard(tx, id)
		if e != nil {
			return e
		}
		if g.ID != id || g.State != "armed" {
			return ErrGuardConflict
		}
		g.State = "canceled"
		g.Before = nil
		g.Plan = nil
		return putGuard(tx, g)
	}, true)
}
func CaptureGuardUnit(path, id string, unit GuardUnit) error {
	return guardDB(path, func(tx *bolt.Tx) error {
		g, e := readGuard(tx, id)
		if e != nil {
			return e
		}
		if g.ID != id || !g.Active() {
			return ErrGuardConflict
		}
		if g.Unit != nil {
			return nil
		}
		unit.Data = append([]byte(nil), unit.Data...)
		g.Unit = &unit
		return putGuard(tx, g)
	}, true)
}

func ApplyGuard(path, id string, now time.Time, fn func(*Config) error) error {
	return guardDB(path, func(tx *bolt.Tx) error {
		g, e := readGuard(tx, id)
		if e != nil {
			return e
		}
		if g.ID != id {
			return ErrGuardConflict
		}
		if g.State == "observing" {
			return nil
		}
		if g.State != "armed" || g.Expired(now) {
			return ErrGuardConflict
		}
		b := tx.Bucket([]byte("config"))
		var c Config
		if b == nil || json.Unmarshal(b.Get([]byte("json")), &c) != nil {
			return ErrConfig
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
		if e = b.Put([]byte("json"), raw); e != nil {
			return e
		}
		g.State = "observing"
		g.AppliedMS = now.UnixMilli()
		g.LastBeatMS = g.AppliedMS
		g.HealthySinceMS = 0
		return putGuard(tx, g)
	}, true)
}

// One-use echoes prove both directions of the encrypted admin stream. Replayed
// requests or a working uplink with a dead downlink cannot extend the trial.
func BeatGuard(path, owner, id, echo string, now time.Time) (Guard, error) {
	var result Guard
	e := guardDB(path, func(tx *bolt.Tx) error {
		g, e := readGuard(tx, id)
		if e != nil {
			return e
		}
		if g.ID != id || g.Owner != owner || g.State != "observing" || g.ActionDoneMS == 0 || g.Expired(now) || echo != g.Challenge || !validGuardID(echo) {
			return ErrGuardConflict
		}
		if g.HealthySinceMS == 0 || now.UnixMilli()-g.LastBeatMS > 15000 {
			g.HealthySinceMS = now.UnixMilli()
		}
		g.LastBeatMS = now.UnixMilli()
		g.Challenge, e = guardNonce()
		if e != nil {
			return e
		}
		if now.UnixMilli()-g.HealthySinceMS >= GuardHealthyTrial.Milliseconds() {
			g.State = "committed"
			g.Before = nil
			g.Unit = nil
			g.Plan = nil
		}
		if e = putGuard(tx, g); e != nil {
			return e
		}
		result = g
		return nil
	}, true)
	return result, e
}

func MarkGuardRollback(path, id string) error {
	return guardDB(path, func(tx *bolt.Tx) error {
		g, e := readGuard(tx, id)
		if e != nil {
			return e
		}
		if g.ID != id || !g.Active() {
			return ErrGuardConflict
		}
		g.State = "rolling_back"
		return putGuard(tx, g)
	}, true)
}

func RollbackGuard(path, id string) error {
	return guardDB(path, func(tx *bolt.Tx) error {
		g, e := readGuard(tx, id)
		if e != nil {
			return e
		}
		if g.ID != id {
			return ErrGuardConflict
		}
		if g.State == "rolled_back" {
			return nil
		}
		if !g.Active() {
			return ErrGuardConflict
		}
		var old, current Config
		b := tx.Bucket([]byte("config"))
		if b == nil || json.Unmarshal(g.Before, &old) != nil || json.Unmarshal(b.Get([]byte("json")), &current) != nil {
			return ErrConfig
		}
		// A published identity stays fenced even when its registration is undone.
		accounts := map[string]bool{}
		for _, a := range old.Accounts {
			accounts[a.ID] = true
		}
		for _, a := range old.Retired {
			accounts[a.ID] = true
		}
		for _, a := range current.Accounts {
			if !accounts[a.ID] {
				old.Retired = append(old.Retired, AccountIdentity{ID: a.ID, Host: a.Mail.Host, Username: a.Mail.Username})
				accounts[a.ID] = true
				old.Schema = max(old.Schema, 2)
			}
		}
		users := map[string]bool{}
		for _, u := range old.Users {
			users[u.ID] = true
		}
		for _, u := range old.RetiredUsers {
			users[u.ID] = true
		}
		for _, u := range current.Users {
			if !users[u.ID] {
				old.RetiredUsers = append(old.RetiredUsers, UserIdentity{ID: u.ID, Up: u.Up, Down: u.Down})
				users[u.ID] = true
				old.Schema = max(old.Schema, 3)
			}
		}
		if current.Notice.Revision > old.Notice.Revision {
			if current.Notice.Revision >= wire.MaxOffset {
				return ErrConfig
			}
			old.Notice.Revision = current.Notice.Revision + 1
			old.Schema = max(old.Schema, 4)
		}
		if e = old.Validate(); e != nil {
			return e
		}
		raw, e := json.Marshal(old)
		if e != nil {
			return e
		}
		if e = b.Put([]byte("json"), raw); e != nil {
			return e
		}
		g.State = "rolled_back"
		g.Before = nil
		g.Unit = nil
		g.Plan = nil
		return putGuard(tx, g)
	}, true)
}

func guardPlanHash(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func CompleteGuardAction(path, id string, now time.Time) error {
	return guardDB(path, func(tx *bolt.Tx) error {
		g, e := readGuard(tx, id)
		if e != nil {
			return e
		}
		if g.ID != id || g.State != "observing" {
			return ErrGuardConflict
		}
		if g.ActionDoneMS == 0 {
			g.ActionDoneMS = now.UnixMilli()
			g.LastBeatMS = g.ActionDoneMS
		}
		return putGuard(tx, g)
	}, true)
}

func FindGuard(path, id string) (Guard, error) {
	var g Guard
	e := guardDB(path, func(tx *bolt.Tx) error { var e error; g, e = readGuard(tx, id); return e }, false)
	return g, e
}
