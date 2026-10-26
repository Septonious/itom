// Package wallet persists client grant consumption before returning a writer
// permit. It stores descriptors, never TCP payload or encryption keys.
package wallet

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
	"itom.local/core/secure"
	"itom.local/core/wire"
)

var ErrState = errors.New("WALLET_STATE")
var ErrMissing = errors.New("WALLET_MISSING")
var ErrLimit = errors.New("WALLET_LIMIT")

// ErrExhausted means a valid open cumulative grant cannot fit this next
// physical message. Spend is fully rolled back in this case, so the caller may
// fence that grant and retry the *same unsent encrypted batch* on another grant.
var ErrExhausted = errors.New("WALLET_EXHAUSTED")
var ErrIO = errors.New("WALLET_IO_UNCERTAIN")

const MaxGrants = 128
const MaxPending = 64

const retiredOfferLimit = 4096

var meta = []byte("meta")
var grants = []byte("grants")
var retiredOffers = []byte("retired_offers")

type Entry struct {
	Offer   wire.StorageGrant
	Used    wire.Quota
	Ack     wire.Quota
	Fenced  bool
	Retired bool
	Pending []wire.UsageReport
}
type Wallet struct {
	metrics          commitMetrics
	mu               sync.Mutex
	db               *bolt.DB
	user             string
	store, session   wire.ID
	failed           bool
	recovered        bool // an existing authenticated store identity survived reopen
	tickets          map[uint64]ticketReservation
	nextTicket       uint64
	fencing          map[wire.QuotaKey]bool
	admissionChanged chan struct{}
	oldSessions      map[wire.ID]bool
	entriesTX        int
	entriesCached    bool
	entriesView      []Entry
}

func key(n uint64) []byte         { b := make([]byte, 8); binary.BigEndian.PutUint64(b, n); return b }
func within(a, b wire.Quota) bool { return a.Bytes <= b.Bytes && a.Messages <= b.Messages }
func put(b *bolt.Bucket, n uint64, e Entry) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return b.Put(key(n), raw)
}
func get(b *bolt.Bucket, k wire.QuotaKey) (Entry, error) {
	var e Entry
	raw := b.Get(key(k.Grant))
	if raw == nil {
		return e, ErrState
	}
	if json.Unmarshal(raw, &e) != nil || e.Offer.Key != k {
		return Entry{}, ErrState
	}
	return e, nil
}

// Open creates a fresh random session and fences ALL previous grants. The caller
// must use Session() in the fresh Noise binding. Reopen never resumes old writes.
func Open(path, user string, store wire.ID) (*Wallet, error) {
	var session wire.ID
	if _, err := rand.Read(session[:]); err != nil {
		return nil, err
	}
	return openSession(path, user, store, session)
}

// OpenBound binds the wallet to the already established fresh Noise session.
// Call only after authenticating CoordinatorInfo, before acknowledging it.
func OpenBound(path string, store wire.ID, epoch *secure.Epoch) (*Wallet, error) {
	if epoch == nil {
		return nil, ErrState
	}
	b := epoch.Context()
	return openSession(path, b.User, store, b.Session)
}
func openSession(path, user string, store, session wire.ID) (*Wallet, error) {
	if session == (wire.ID{}) || len(user) != 8 || user[0] == '0' || store == (wire.ID{}) {
		return nil, ErrState
	}
	for _, c := range user {
		if c < '0' || c > '9' {
			return nil, ErrState
		}
	}
	if st, e := os.Lstat(path); e == nil {
		if !st.Mode().IsRegular() || runtime.GOOS != "windows" && st.Mode().Perm()&0077 != 0 {
			return nil, ErrState
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	db, e := bolt.Open(path, 0600, &bolt.Options{Timeout: time.Second})
	if e != nil {
		return nil, e
	}
	w := &Wallet{db: db, user: user, store: store, session: session}
	e = db.Update(func(tx *bolt.Tx) error {
		m := tx.Bucket(meta)
		w.recovered = m != nil
		if m == nil {
			if k, _ := tx.Cursor().First(); k != nil {
				return ErrState
			}
			var err error
			m, err = tx.CreateBucket(meta)
			if err != nil {
				return err
			}
			if _, err = tx.CreateBucket(grants); err != nil {
				return err
			}
			if err = m.Put([]byte("identity"), append(store[:], []byte(user)...)); err != nil {
				return err
			}
			if err = m.Put([]byte("schema"), key(2)); err != nil {
				return err
			}
			if err = m.Put([]byte("high"), key(0)); err != nil {
				return err
			}
		}
		if string(m.Get([]byte("schema"))) != string(key(1)) && string(m.Get([]byte("schema"))) != string(key(2)) {
			return ErrState
		}
		if string(m.Get([]byte("identity"))) != string(append(store[:], []byte(user)...)) || len(m.Get([]byte("high"))) != 8 {
			return ErrState
		}
		b := tx.Bucket(grants)
		if b == nil {
			return ErrState
		}
		if previous := m.Get([]byte("session")); len(previous) > 0 && (len(previous) != 16 || string(previous) == string(session[:])) {
			return ErrState
		}
		// All unknown offers below the previous session's high-water mark stay
		// fenced, including wallets upgraded without retired-offer tombstones.
		// Current-session retirement records then distinguish replay from a
		// genuinely unseen offer delivered out of order by a replica.
		if err := m.Put([]byte("offer_floor"), append([]byte(nil), m.Get([]byte("high"))...)); err != nil {
			return err
		}
		if tx.Bucket(retiredOffers) != nil {
			if err := tx.DeleteBucket(retiredOffers); err != nil {
				return err
			}
		}
		if _, err := tx.CreateBucket(retiredOffers); err != nil {
			return err
		}
		var entries []Entry
		// Only this wallet's durable session history can attest that an unseen
		// offer was never spent. A different device using the same user key must
		// not fence another device's unknown allocation as zero.
		w.oldSessions = make(map[wire.ID]bool)
		var history []wire.ID
		if raw := m.Get([]byte("session_history")); len(raw) > 0 {
			if json.Unmarshal(raw, &history) != nil || len(history) > 256 {
				return ErrState
			}
			for _, id := range history {
				if id == (wire.ID{}) || id == session {
					return ErrState
				}
			}
		}
		if previous := m.Get([]byte("session")); len(previous) == 16 {
			var id wire.ID
			copy(id[:], previous)
			if id == (wire.ID{}) {
				return ErrState
			}
			w.oldSessions[id] = true
		}
		err := b.ForEach(func(k, v []byte) error {
			var x Entry
			if len(k) != 8 || len(entries) >= MaxGrants || json.Unmarshal(v, &x) != nil {
				return ErrState
			}
			if x.Offer.Key.Session == session {
				return ErrState
			}
			if x.Offer.Key.User != user || x.Offer.Key.Store != store || x.Offer.Key.Grant != binary.BigEndian.Uint64(k) || x.Offer.Key.Grant > binary.BigEndian.Uint64(m.Get([]byte("high"))) || !within(x.Used, x.Offer.Limit) || !within(x.Ack, x.Used) || len(x.Pending) > MaxPending {
				return ErrState
			}
			if _, err := wire.EncodeControl(0, x.Offer); err != nil {
				return ErrState
			}
			prev := x.Ack
			for _, p := range x.Pending {
				if p.Key != x.Offer.Key || p.Attempt == nil || p.Total.Messages != prev.Messages+1 || p.Total.Bytes != prev.Bytes+p.Attempt.EncodedBytes {
					return ErrState
				}
				if _, err := wire.EncodeControl(0, p); err != nil {
					return ErrState
				}
				prev = p.Total
			}
			if x.Retired && (!x.Fenced || x.Ack != x.Used || len(x.Pending) != 0) {
				return ErrState
			}
			if prev != x.Used {
				return ErrState
			}
			x.Fenced = true
			w.oldSessions[x.Offer.Key.Session] = true
			entries = append(entries, x)
			return nil
		})
		if err != nil {
			return err
		}
		if err := m.Put([]byte("schema"), key(2)); err != nil {
			return err
		}
		if err := m.Put([]byte("session"), session[:]); err != nil {
			return err
		}
		for i := len(history) - 1; i >= 0 && len(w.oldSessions) < 256; i-- {
			w.oldSessions[history[i]] = true
		}
		retained := make([]wire.ID, 0, len(w.oldSessions))
		for id := range w.oldSessions {
			retained = append(retained, id)
		}
		rawHistory, err := json.Marshal(retained)
		if err != nil {
			return err
		}
		if err = m.Put([]byte("session_history"), rawHistory); err != nil {
			return err
		}
		for _, x := range entries {
			if err = put(b, x.Offer.Key.Grant, x); err != nil {
				return err
			}
		}
		return nil
	})
	if e == nil && runtime.GOOS != "windows" {
		var d *os.File
		d, e = os.Open(filepath.Dir(path))
		if e == nil {
			e = d.Sync()
			ce := d.Close()
			if e == nil {
				e = ce
			}
		}
	}
	if e != nil {
		db.Close()
		return nil, e
	}
	return w, nil
}
func (w *Wallet) Session() wire.ID { return w.session }
func (w *Wallet) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.failed = true
	w.entriesView = nil
	return w.db.Close()
}
func (w *Wallet) write(fn func(*bolt.Tx) error) error { return w.writeAfter(fn, nil) }
func (w *Wallet) writeAfter(fn func(*bolt.Tx) error, after func()) error {
	queued := time.Now()
	w.mu.Lock()
	w.metrics.queueNS.Add(uint64(time.Since(queued)))
	defer w.mu.Unlock()
	if w.failed {
		return ErrIO
	}
	started := w.metrics.timeline.Begin()
	begin := time.Now()
	e := w.db.Update(fn)
	if e == nil && after != nil {
		after()
	}
	sequence := w.metrics.count.Add(1)
	w.metrics.timeline.Finish(sequence, started, e != nil)
	elapsed := uint64(time.Since(begin))
	w.metrics.commitNS.Add(elapsed)
	for old := w.metrics.maxNS.Load(); elapsed > old; old = w.metrics.maxNS.Load() {
		if w.metrics.maxNS.CompareAndSwap(old, elapsed) {
			break
		}
	}
	if e != nil {
		w.metrics.failures.Add(1)
	}
	if e != nil && !errors.Is(e, ErrState) && !errors.Is(e, ErrMissing) && !errors.Is(e, ErrLimit) && !errors.Is(e, ErrExhausted) {
		w.failed = true
		w.signalAdmissionLocked()
	}
	return e
}

// Accept is called only after authenticated server control validation. Duplicate
// offers never renew Used. Retired offers cannot reappear; unseen current-session
// offers may arrive out of order because allocation order is not visibility order.
func (w *Wallet) Accept(o wire.StorageGrant) error {
	_, known, err := w.acceptOffer(o)
	if err != nil {
		return err
	}
	if !known {
		return ErrState
	}
	return nil
}

// A false result is an obsolete/retired offer, not new spending authority.
// Runtime callers must not queue that key. The strict Accept API still rejects it.
func (w *Wallet) AcceptCurrentOffer(o wire.StorageGrant) (accepted bool, err error) {
	accepted, _, err = w.acceptOffer(o)
	return accepted, err
}

func (w *Wallet) acceptOffer(o wire.StorageGrant) (accepted, known bool, err error) {
	if _, e := wire.EncodeControl(0, o); e != nil {
		return false, false, ErrState
	}
	if o.Key.Store != w.store || o.Key.User != w.user {
		return false, false, ErrState
	}
	err = w.write(func(tx *bolt.Tx) error {
		b := tx.Bucket(grants)
		m := tx.Bucket(meta)
		if raw := b.Get(key(o.Key.Grant)); raw != nil {
			var x Entry
			if json.Unmarshal(raw, &x) != nil || x.Offer != o {
				return ErrState
			}
			accepted = !x.Fenced && !x.Retired && o.Key.Session == w.session
			known = true
			return nil
		}
		if o.Key.Session != w.session || o.Key.Grant <= binary.BigEndian.Uint64(m.Get([]byte("offer_floor"))) || tx.Bucket(retiredOffers).Get(key(o.Key.Grant)) != nil {
			return nil
		}
		if b.Stats().KeyN >= MaxGrants {
			return ErrLimit
		}
		if e := put(b, o.Key.Grant, Entry{Offer: o}); e != nil {
			return e
		}
		accepted = true
		known = true
		if o.Key.Grant > binary.BigEndian.Uint64(m.Get([]byte("high"))) {
			return m.Put([]byte("high"), key(o.Key.Grant))
		}
		return nil
	})
	return accepted, known, err
}

// Spend commits lifetime usage before returning body and immutable descriptor.
// A failed/uncertain return authorizes NO I/O. Retrying this API consumes a NEW
// attempt; retransmitting its report never authorizes another APPEND.
func (w *Wallet) Spend(k wire.QuotaKey, folder string, encrypted []byte, encodedSize func([]byte) (uint64, error)) (wire.UsageReport, []byte, error) {
	return w.spend(k, folder, encrypted, nil, encodedSize, 0)
}

// SpendEnvelope retains exactly the existing durable spend/descriptor/restart
// contract while carrying authenticated registration metadata in DATA itself.
func (w *Wallet) SpendEnvelope(k wire.QuotaKey, folder string, encrypted, authenticationKey []byte, encodedSize func([]byte) (uint64, error)) (wire.UsageReport, []byte, error) {
	if len(authenticationKey) != 32 {
		return wire.UsageReport{}, nil, ErrState
	}
	return w.spend(k, folder, encrypted, authenticationKey, encodedSize, 0)
}

func (w *Wallet) spend(k wire.QuotaKey, folder string, encrypted, authenticationKey []byte, encodedSize func([]byte) (uint64, error), ticket uint64) (wire.UsageReport, []byte, error) {
	var report wire.UsageReport
	var body []byte
	e := w.writeAfter(func(tx *bolt.Tx) error {
		b := tx.Bucket(grants)
		x, e := get(b, k)
		if e != nil {
			return e
		}
		if x.Fenced || k.Session != w.session {
			return ErrState
		}
		if ticket == 0 && w.fencing[k] {
			return ErrExhausted
		}
		if len(x.Pending) >= MaxPending {
			return ErrLimit
		}
		var batch wire.ID
		if _, e = rand.Read(batch[:]); e != nil {
			return e
		}
		if authenticationKey == nil {
			body, e = wire.BindPhysical(k, x.Used.Messages+1, batch, encrypted)
		} else {
			report, body, e = wire.BindDataEnvelope(k, x.Used.Messages+1, batch, x.Used.Bytes, folder, encrypted, authenticationKey, encodedSize)
		}
		if e != nil {
			return ErrLimit
		}
		n, e := encodedSize(body)
		if e != nil {
			return e
		}
		if authenticationKey != nil && n != report.Attempt.EncodedBytes {
			return ErrState
		}
		total := wire.Quota{Bytes: x.Used.Bytes + n, Messages: x.Used.Messages + 1}
		if n > 2<<20 {
			return ErrLimit
		}
		if ticket != 0 {
			reservation, ok := w.tickets[ticket]
			if !ok || reservation.key != k || n > reservation.bytes {
				return ErrState
			}
		}
		reserved, messages := w.reservedLocked(k, ticket)
		if !within(total, x.Offer.Limit) || reserved > x.Offer.Limit.Bytes-total.Bytes || messages > x.Offer.Limit.Messages-total.Messages {
			return ErrExhausted
		}
		if len(x.Pending)+int(messages) >= MaxPending {
			return ErrLimit
		}
		report = wire.UsageReport{Key: k, Total: total, Attempt: &wire.PhysicalDescriptor{Batch: batch, EncodedBytes: n, DecodedBytes: uint64(len(body)), Digest: sha256.Sum256(body), Folder: folder}}
		if _, e = wire.EncodeControl(0, report); e != nil {
			return ErrState
		}
		x.Used = total
		x.Pending = append(x.Pending, report)
		if err := put(b, k.Grant, x); err != nil {
			return err
		}
		// Deletion must follow successful DB commit, outside this callback.
		return nil
	}, func() {
		if ticket != 0 {
			delete(w.tickets, ticket)
			w.signalAdmissionLocked()
		}
	})
	if e != nil {
		return wire.UsageReport{}, nil, e
	}
	return report, body, nil
}
func (w *Wallet) Fence(k wire.QuotaKey) (wire.StorageRelease, error) {
	var out wire.StorageRelease
	e := w.write(func(tx *bolt.Tx) error {
		b := tx.Bucket(grants)
		x, e := get(b, k)
		if e != nil {
			return e
		}
		x.Fenced = true
		out = wire.StorageRelease{Key: k, Final: x.Used}
		return put(b, k.Grant, x)
	})
	if e != nil {
		return wire.StorageRelease{}, e
	}
	return out, nil
}

// Acknowledge consumes only authenticated coordinator status. It prunes the
// registered descriptor prefix, not the lifetime debit. ErrMissing elsewhere
// is never substituted for a final cleanup acknowledgement.
func (w *Wallet) Acknowledge(s wire.CleanupStatus) error {
	if _, e := wire.EncodeControl(0, s); e != nil {
		return ErrState
	}
	return w.write(func(tx *bolt.Tx) error {
		b := tx.Bucket(grants)
		if b.Get(key(s.Key.Grant)) == nil {
			return ErrMissing
		}
		x, e := get(b, s.Key)
		if e != nil {
			return e
		}
		if !within(s.Used, x.Used) || s.Closed && (!x.Fenced || s.Used != x.Used) {
			return ErrState
		}
		if within(s.Registered, x.Ack) {
			if x.Fenced && s.Closed && s.Released == x.Used && s.Registered == x.Ack {
				x.Retired = true
				return put(b, s.Key.Grant, x)
			}
			return nil
		}
		if !within(x.Ack, s.Registered) || !within(s.Registered, x.Used) {
			return ErrState
		}
		i := 0
		for i < len(x.Pending) && x.Pending[i].Total.Messages <= s.Registered.Messages {
			i++
		}
		if i == 0 || x.Pending[i-1].Total != s.Registered {
			return ErrState
		}
		x.Ack = s.Registered
		x.Pending = append([]wire.UsageReport(nil), x.Pending[i:]...)
		if x.Fenced && s.Closed && s.Released == x.Used {
			x.Retired = true
			return put(b, s.Key.Grant, x)
		}
		return put(b, s.Key.Grant, x)
	})
}
func (w *Wallet) Entries() ([]Entry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failed {
		return nil, ErrIO
	}
	e := w.db.View(func(tx *bolt.Tx) error {
		// A committed bbolt transaction is immutable. Reuse only this exact
		// snapshot, never an estimated grant balance or stale mutable entry.
		if w.entriesCached && w.entriesTX == tx.ID() {
			return nil
		}
		var view []Entry
		err := tx.Bucket(grants).ForEach(func(_, v []byte) error {
			var x Entry
			if len(view) >= MaxGrants || json.Unmarshal(v, &x) != nil {
				return ErrState
			}
			view = append(view, x)
			return nil
		})
		if err == nil {
			w.entriesTX, w.entriesCached, w.entriesView = tx.ID(), true, view
		}
		return err
	})
	if e != nil {
		return nil, e
	}
	// Callers may retain/edit their detached inspection result. No mutable
	// Pending slice or descriptor pointer can alias the next policy read.
	out := append([]Entry(nil), w.entriesView...)
	for i := range out {
		out[i].Pending = append([]wire.UsageReport(nil), out[i].Pending...)
		for j := range out[i].Pending {
			if a := out[i].Pending[j].Attempt; a != nil {
				copy := *a
				out[i].Pending[j].Attempt = &copy
			}
		}
	}
	return out, nil
}

// Retirements is a durable outbox. A new process retransmits it after startup
// authentication; unlike a storage grant it can reconcile its original session.
func (w *Wallet) Retirements() ([]wire.Retirement, error) {
	entries, e := w.Entries()
	if e != nil {
		return nil, e
	}
	var out []wire.Retirement
	for _, x := range entries {
		if x.Retired {
			out = append(out, wire.Retirement{Key: x.Offer.Key, Final: x.Used})
		}
	}
	return out, nil
}
func (w *Wallet) CompleteRetirement(r wire.Retirement) error {
	if _, e := wire.EncodeControl(0, r); e != nil || !r.Ack || r.Key.Store != w.store || r.Key.User != w.user {
		return ErrState
	}
	return w.write(func(tx *bolt.Tx) error {
		b := tx.Bucket(grants)
		if b.Get(key(r.Key.Grant)) == nil {
			return nil
		} // duplicate acknowledgement; never a proof
		x, e := get(b, r.Key)
		if e != nil {
			return e
		}
		if !x.Retired || !x.Fenced || x.Used != r.Final || x.Ack != x.Used {
			return ErrState
		}
		tombstones := tx.Bucket(retiredOffers)
		atLimit := tombstones.Stats().KeyN >= retiredOfferLimit && tombstones.Get(key(r.Key.Grant)) == nil
		if err := tombstones.Put(key(r.Key.Grant), []byte{1}); err != nil {
			return err
		}
		if atLimit {
			oldest, _ := tombstones.Cursor().First()
			floor := binary.BigEndian.Uint64(oldest)
			m := tx.Bucket(meta)
			if floor > binary.BigEndian.Uint64(m.Get([]byte("offer_floor"))) {
				if err := m.Put([]byte("offer_floor"), key(floor)); err != nil {
					return err
				}
			}
			if err := tombstones.Delete(oldest); err != nil {
				return err
			}
		}
		return b.Delete(key(r.Key.Grant))
	})
}

// Retirement returns only the specified durable record. Live cleanup handling
// must not replay the entire outbox for every acknowledgement (quadratic load).
func (w *Wallet) Retirement(k wire.QuotaKey) (*wire.Retirement, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failed {
		return nil, ErrIO
	}
	var result *wire.Retirement
	err := w.db.View(func(tx *bolt.Tx) error {
		x, e := get(tx.Bucket(grants), k)
		if e != nil {
			return e
		}
		if x.Retired {
			result = &wire.Retirement{Key: x.Offer.Key, Final: x.Used}
		}
		return nil
	})
	return result, err
}
