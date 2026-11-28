package v160

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

const (
	MaxSessions           = 64 // includes retained/uncollected sessions
	MaxUserSessions       = 8
	MaxCandidates         = 16
	MaxFolders            = 8192
	MaxAccountFolders     = 512
	AccountByteLimit      = 64 << 20
	AccountControlReserve = 2 << 20
)

var usersBucket = []byte("users")
var sessionsBucket = []byte("sessions")
var foldersBucket = []byte("folders")
var accountsBucket = []byte("accounts")

type SessionStage uint8

const (
	Connecting SessionStage = iota + 1
	Active
	Fenced
)

type Lease struct {
	Key   Key
	Epoch [16]byte
}
type sessionState struct {
	Lease          Lease
	Stage          SessionStage
	NextGeneration uint64
	Folders        int
}
type SlotStage uint8

const (
	Prepared SlotStage = iota + 1
	Writing
	Sealed
)

type FolderLease struct {
	Lease      Lease
	Account    string
	Folder     Folder
	Generation uint64
}
type AppendState uint8

const (
	Reserved AppendState = iota + 1
	Confirmed
	Uncertain
)

type publication struct {
	Bytes     int
	State     AppendState
	UID       uint32
	Receipted bool
}
type slotState struct {
	Lease    FolderLease
	Stage    SlotStage
	Validity uint32
	Next     uint64
	Bytes    int
	Items    map[uint64]publication
}
type accountState struct{ Bytes, Folders int }
type Reservation struct {
	Folder FolderLease
	ID     uint64
}
type Usage struct{ Messages, Reserved, Confirmed, Uncertain, Bytes int }

// Registry is the durable single-owner ledger for one endpoint. It is not an
// IMAP client. Provider assertions must originate from exclusive CREATE, an
// audited UID inventory and a stopped/fenced producer, never a name match.
// Bolt's exclusive file lock prevents concurrent local owners. On reopening,
// all previously live leases are fenced, and pending appends remain uncertain.
type Registry struct{ db *bolt.DB }

func OpenRegistry(path string) (*Registry, error) {
	db, e := bolt.Open(path, 0600, &bolt.Options{Timeout: time.Second})
	if e != nil {
		return nil, e
	}
	r := &Registry{db}
	e = db.Update(func(tx *bolt.Tx) error {
		for _, b := range [][]byte{usersBucket, sessionsBucket, foldersBucket, accountsBucket} {
			if _, e := tx.CreateBucketIfNotExists(b); e != nil {
				return e
			}
		}
		s := tx.Bucket(sessionsBucket)
		var keys [][]byte
		if e := s.ForEach(func(k, v []byte) error { keys = append(keys, append([]byte(nil), k...)); return nil }); e != nil {
			return e
		}
		for _, k := range keys {
			var st sessionState
			if e := load(s, k, &st); e != nil {
				return e
			}
			st.Stage = Fenced
			if e := save(s, k, st); e != nil {
				return e
			}
		}
		f := tx.Bucket(foldersBucket)
		keys = nil
		if e := f.ForEach(func(k, v []byte) error { keys = append(keys, append([]byte(nil), k...)); return nil }); e != nil {
			return e
		}
		for _, k := range keys {
			var st slotState
			if e := load(f, k, &st); e != nil {
				return e
			}
			for id, p := range st.Items {
				if p.State == Reserved {
					p.State = Uncertain
					st.Items[id] = p
				}
			}
			if e := save(f, k, st); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		db.Close()
		return nil, e
	}
	return r, nil
}
func (r *Registry) Close() error { return r.db.Close() }
func load(b *bolt.Bucket, k []byte, v any) error {
	raw := b.Get(k)
	if raw == nil {
		return ErrState
	}
	if e := json.Unmarshal(raw, v); e != nil {
		return fmt.Errorf("v160 journal: %w", e)
	}
	return nil
}
func save(b *bolt.Bucket, k []byte, v any) error {
	raw, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return b.Put(k, raw)
}
func sessionKey(k Key) []byte {
	return binary.BigEndian.AppendUint32([]byte(k.User), uint32(k.Session))
}
func folderKey(account string, f Folder) []byte {
	n, _ := f.Name()
	return []byte(account + "\x00" + n)
}
func validAccount(a string) bool { return len(a) > 0 && len(a) <= 128 && !strings.ContainsRune(a, 0) }
func (r *Registry) RegisterUser(id UserID) error {
	if !id.Valid() {
		return ErrProtocol
	}
	return r.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(usersBucket)
		if b.Get([]byte(id)) != nil {
			return ErrConflict
		}
		return b.Put([]byte(id), []byte{1})
	})
}
func (r *Registry) AllocateUser() (UserID, error) {
	for range 128 {
		id, e := RandomUser()
		if e != nil {
			return "", e
		}
		e = r.RegisterUser(id)
		if e == ErrConflict {
			continue
		}
		return id, e
	}
	return "", ErrCapacity
}
func (r *Registry) Begin(k Key) (Lease, error) {
	if !k.Valid() {
		return Lease{}, ErrProtocol
	}
	l := Lease{Key: k}
	if _, e := rand.Read(l.Epoch[:]); e != nil {
		return Lease{}, e
	}
	e := r.db.Update(func(tx *bolt.Tx) error {
		if tx.Bucket(usersBucket).Get([]byte(k.User)) == nil {
			return ErrState
		}
		b := tx.Bucket(sessionsBucket)
		if b.Get(sessionKey(k)) != nil {
			return ErrConflict
		}
		total, user, candidates := 0, 0, 0
		if e := b.ForEach(func(_, v []byte) error {
			var st sessionState
			if e := json.Unmarshal(v, &st); e != nil {
				return e
			}
			total++
			if st.Lease.Key.User == k.User {
				user++
			}
			if st.Stage == Connecting {
				candidates++
			}
			return nil
		}); e != nil {
			return e
		}
		if total >= MaxSessions || user >= MaxUserSessions || candidates >= MaxCandidates {
			return ErrCapacity
		}
		return save(b, sessionKey(k), sessionState{Lease: l, Stage: Connecting})
	})
	if e != nil {
		return Lease{}, e
	}
	return l, nil
}
func session(tx *bolt.Tx, l Lease) (sessionState, error) {
	var st sessionState
	if e := load(tx.Bucket(sessionsBucket), sessionKey(l.Key), &st); e != nil {
		return st, e
	}
	if st.Lease != l {
		return st, ErrConflict
	}
	return st, nil
}

// Activate is called after authenticated READY and admission, never on HELLO.
func (r *Registry) Activate(l Lease) error {
	return r.db.Update(func(tx *bolt.Tx) error {
		st, e := session(tx, l)
		if e != nil {
			return e
		}
		if st.Stage != Connecting {
			return ErrState
		}
		st.Stage = Active
		return save(tx.Bucket(sessionsBucket), sessionKey(l.Key), st)
	})
}
func (r *Registry) Fence(l Lease) error {
	return r.db.Update(func(tx *bolt.Tx) error {
		st, e := session(tx, l)
		if e != nil {
			return e
		}
		st.Stage = Fenced
		return save(tx.Bucket(sessionsBucket), sessionKey(l.Key), st)
	})
}
func (r *Registry) Release(l Lease) error {
	return r.db.Update(func(tx *bolt.Tx) error {
		st, e := session(tx, l)
		if e != nil {
			return e
		}
		if st.Stage != Fenced || st.Folders != 0 {
			return ErrState
		}
		return tx.Bucket(sessionsBucket).Delete(sessionKey(l.Key))
	})
}
func account(tx *bolt.Tx, id string) (accountState, error) {
	var a accountState
	b := tx.Bucket(accountsBucket)
	if b.Get([]byte(id)) == nil {
		return a, nil
	}
	e := load(b, []byte(id), &a)
	return a, e
}

// Prepare durably records an exclusive creation intent before issuing CREATE.
// An ambiguous CREATE remains owned as an unresolved intent, not writable.
func (r *Registry) Prepare(l Lease, acct string, f Folder) (FolderLease, error) {
	var fl FolderLease
	if !validAccount(acct) || !f.Valid() || f.Key != l.Key {
		return fl, ErrProtocol
	}
	e := r.db.Update(func(tx *bolt.Tx) error {
		st, e := session(tx, l)
		if e != nil {
			return e
		}
		if st.Stage != Active && (st.Stage != Connecting || f.Type.Capacity() != 512) {
			return ErrState
		}
		b := tx.Bucket(foldersBucket)
		k := folderKey(acct, f)
		if b.Get(k) != nil {
			return ErrConflict
		}
		if b.Stats().KeyN >= MaxFolders {
			return ErrCapacity
		}
		a, e := account(tx, acct)
		if e != nil {
			return e
		}
		if a.Folders >= MaxAccountFolders || st.NextGeneration == ^uint64(0) {
			return ErrCapacity
		}
		st.NextGeneration++
		st.Folders++
		a.Folders++
		fl = FolderLease{l, acct, f, st.NextGeneration}
		if e = save(b, k, slotState{Lease: fl, Stage: Prepared, Items: map[uint64]publication{}}); e != nil {
			return e
		}
		if e = save(tx.Bucket(sessionsBucket), sessionKey(l.Key), st); e != nil {
			return e
		}
		return save(tx.Bucket(accountsBucket), []byte(acct), a)
	})
	if e != nil {
		return FolderLease{}, e
	}
	return fl, nil
}
func slot(tx *bolt.Tx, fl FolderLease) (slotState, error) {
	var st slotState
	if !fl.Folder.Valid() || !validAccount(fl.Account) || fl.Folder.Key != fl.Lease.Key {
		return st, ErrProtocol
	}
	if _, e := session(tx, fl.Lease); e != nil {
		return st, e
	}
	if e := load(tx.Bucket(foldersBucket), folderKey(fl.Account, fl.Folder), &st); e != nil {
		return st, e
	}
	if st.Lease != fl {
		return st, ErrConflict
	}
	return st, nil
}
func putSlot(tx *bolt.Tx, st slotState) error {
	return save(tx.Bucket(foldersBucket), folderKey(st.Lease.Account, st.Lease.Folder), st)
}

// ConfirmCreated requires a provider-confirmed exclusive CREATE followed by
// SELECT with zero messages. Never call on an existing name or ambiguous reply.
func (r *Registry) ConfirmCreated(fl FolderLease, validity uint32, messages int) error {
	if validity == 0 || messages != 0 {
		return ErrProtocol
	}
	return r.db.Update(func(tx *bolt.Tx) error {
		st, e := slot(tx, fl)
		if e != nil {
			return e
		}
		s, e := session(tx, fl.Lease)
		if e != nil {
			return e
		}
		if st.Stage != Prepared || s.Stage == Fenced {
			return ErrState
		}
		st.Stage = Writing
		st.Validity = validity
		return putSlot(tx, st)
	})
}

// Reserve atomically charges one physical message per supplied encoded byte
// length. This is intentionally batchable: durable commit precedes publication.
// The caller must include full provider-stored bytes, not plaintext lengths.
func (r *Registry) Reserve(fl FolderLease, sizes []int, recovery bool) ([]Reservation, error) {
	if len(sizes) == 0 || len(sizes) > 512 {
		return nil, ErrCapacity
	}
	total := 0
	for _, n := range sizes {
		if n <= 0 || n > MaxContainer*2+4096 {
			return nil, ErrProtocol
		}
		total += n
	}
	var out []Reservation
	e := r.db.Update(func(tx *bolt.Tx) error {
		st, e := slot(tx, fl)
		if e != nil {
			return e
		}
		s, e := session(tx, fl.Lease)
		if e != nil {
			return e
		}
		if st.Stage != Writing || s.Stage == Fenced {
			return ErrState
		}
		limit := fl.Folder.Type.OrdinaryLimit()
		if recovery && fl.Folder.Type.Capacity() == 512 {
			limit = 512
		}
		if len(st.Items)+len(sizes) > limit || st.Next > ^uint64(0)-uint64(len(sizes)) {
			return ErrCapacity
		}
		a, e := account(tx, fl.Account)
		if e != nil {
			return e
		}
		budget := AccountByteLimit
		if fl.Folder.Type.Capacity() == 256 {
			budget -= AccountControlReserve
		}
		if a.Bytes > budget-total {
			return ErrCapacity
		}
		for _, n := range sizes {
			st.Next++
			st.Items[st.Next] = publication{Bytes: n, State: Reserved}
			out = append(out, Reservation{fl, st.Next})
		}
		st.Bytes += total
		a.Bytes += total
		if e = putSlot(tx, st); e != nil {
			return e
		}
		return save(tx.Bucket(accountsBucket), []byte(fl.Account), a)
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}
func (r *Registry) resolve(res Reservation, state AppendState, validity, uid uint32) error {
	return r.db.Update(func(tx *bolt.Tx) error {
		st, e := slot(tx, res.Folder)
		if e != nil {
			return e
		}
		p, ok := st.Items[res.ID]
		if !ok {
			return ErrState
		}
		switch state {
		case Confirmed:
			if validity == 0 || validity != st.Validity || uid == 0 {
				return ErrConflict
			}
			if p.State == Confirmed {
				if p.UID == uid {
					return nil
				}
				return ErrConflict
			}
			for id, q := range st.Items {
				if id != res.ID && q.UID == uid {
					return ErrConflict
				}
			}
			p.UID = uid
		case Uncertain:
			if p.State == Confirmed {
				return ErrState
			}
		default:
			return ErrProtocol
		}
		p.State = state
		st.Items[res.ID] = p
		return putSlot(tx, st)
	})
}
func (r *Registry) ConfirmAppend(res Reservation, validity, uid uint32) error {
	return r.resolve(res, Confirmed, validity, uid)
}
func (r *Registry) UnknownAppend(res Reservation) error { return r.resolve(res, Uncertain, 0, 0) }

// CancelUnsent is legal only when the local queue owner proves no APPEND was
// started. An uncertain result is deliberately not refundable through this API.
func (r *Registry) CancelUnsent(res Reservation) error {
	return r.db.Update(func(tx *bolt.Tx) error {
		st, e := slot(tx, res.Folder)
		if e != nil {
			return e
		}
		p, ok := st.Items[res.ID]
		if !ok || p.State != Reserved {
			return ErrState
		}
		a, e := account(tx, res.Folder.Account)
		if e != nil {
			return e
		}
		if a.Bytes < p.Bytes || st.Bytes < p.Bytes {
			return ErrState
		}
		delete(st.Items, res.ID)
		st.Bytes -= p.Bytes
		a.Bytes -= p.Bytes
		if e = putSlot(tx, st); e != nil {
			return e
		}
		return save(tx.Bucket(accountsBucket), []byte(res.Folder.Account), a)
	})
}
func (r *Registry) Receipt(res Reservation) error {
	return r.db.Update(func(tx *bolt.Tx) error {
		st, e := slot(tx, res.Folder)
		if e != nil {
			return e
		}
		p, ok := st.Items[res.ID]
		if !ok {
			return ErrState
		}
		p.Receipted = true
		st.Items[res.ID] = p
		return putSlot(tx, st)
	})
}

// Seal is a producer fence assertion by the caller: no queued/in-progress
// APPEND can remain. For normal rotation all records must be received. During
// session retirement an authenticated session fence may instead discard data.
func (r *Registry) Seal(fl FolderLease, producerStopped bool) error {
	if !producerStopped {
		return ErrState
	}
	return r.db.Update(func(tx *bolt.Tx) error {
		st, e := slot(tx, fl)
		if e != nil {
			return e
		}
		s, e := session(tx, fl.Lease)
		if e != nil {
			return e
		}
		if st.Stage == Sealed {
			return nil
		}
		if st.Stage != Writing {
			return ErrState
		}
		for id, p := range st.Items {
			if s.Stage != Fenced && (!p.Receipted || p.State == Reserved) {
				return ErrState
			}
			if p.State == Reserved {
				p.State = Uncertain
				st.Items[id] = p
			}
		}
		st.Stage = Sealed
		return putSlot(tx, st)
	})
}

// VerifyDeleted takes an actual provider absence proof after the writer fence.
// An unresolved CREATE intent is releasable only after confirmed absence; if
// the name exists it remains quarantined and must not be deleted by name alone.
func (r *Registry) VerifyDeleted(fl FolderLease, absent bool) error {
	if !absent {
		return ErrState
	}
	return r.db.Update(func(tx *bolt.Tx) error {
		st, e := slot(tx, fl)
		if e != nil {
			return e
		}
		s, e := session(tx, fl.Lease)
		if e != nil {
			return e
		}
		if st.Stage != Sealed && !(st.Stage == Prepared && s.Stage == Fenced) {
			return ErrState
		}
		a, e := account(tx, fl.Account)
		if e != nil {
			return e
		}
		if a.Bytes < st.Bytes || a.Folders <= 0 || s.Folders <= 0 {
			return ErrState
		}
		a.Bytes -= st.Bytes
		a.Folders--
		s.Folders--
		if e = tx.Bucket(foldersBucket).Delete(folderKey(fl.Account, fl.Folder)); e != nil {
			return e
		}
		if e = save(tx.Bucket(sessionsBucket), sessionKey(fl.Lease.Key), s); e != nil {
			return e
		}
		return save(tx.Bucket(accountsBucket), []byte(fl.Account), a)
	})
}
func (r *Registry) Usage(fl FolderLease) (Usage, error) {
	var u Usage
	e := r.db.View(func(tx *bolt.Tx) error {
		st, e := slot(tx, fl)
		if e != nil {
			return e
		}
		u.Bytes = st.Bytes
		u.Messages = len(st.Items)
		for _, p := range st.Items {
			switch p.State {
			case Reserved:
				u.Reserved++
			case Confirmed:
				u.Confirmed++
			case Uncertain:
				u.Uncertain++
			}
		}
		return nil
	})
	return u, e
}
