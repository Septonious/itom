package journal

import (
	"crypto/sha256"
	"sync"

	bolt "go.etcd.io/bbolt"
	"itom.local/core/pool"
	"itom.local/core/wire"
)

// LocalTicket reserves a concrete, durable, one-body local grant BEFORE any
// records are drained or encrypted. Crash leaves the reservation held. Closing
// an unused grant may return unused allocation; it never releases a started
// attempt. Ticket methods run on the metadata worker, not under a session lock.
type LocalTicket struct {
	mu         sync.Mutex
	store      *Store
	grant      Grant
	folder     string
	consumed   bool
	MaxEncoded uint64
}

// ReserveLocalSized fits a quantum to the lesser of the existing account and
// protected user allowance, including ALL retained local/remote reservations.
// size must be pure, monotonic and nonblocking. Existing grant/attempt limits
// and the inventory gate remain authoritative; no additional quota is created.
func (s *Store) ReserveLocalSized(scope Scope, folder string, minimum, maximum int, size func(int) (uint64, error)) (*LocalTicket, int, error) {
	if minimum < 0 || maximum < minimum || maximum > MaxDecoded || size == nil {
		return nil, 0, ErrLimit
	}
	floor, err := size(minimum)
	if err != nil {
		return nil, 0, err
	}
	ceiling, err := size(maximum)
	if err != nil {
		return nil, 0, err
	}
	if floor == 0 || floor > ceiling || ceiling > MaxEncoded {
		return nil, 0, ErrLimit
	}
	var grant Grant
	quantum := minimum
	err = s.write(func(tx *bolt.Tx) error {
		a, u, err := s.owner(scope.Account, scope.User)
		if err != nil {
			return err
		}
		if !s.ownsFolder(scope, folder) || scope.Session == (wire.ID{}) {
			return ErrScope
		}
		if !s.ready[ownerKey(scope.Account, scope.User)] {
			return ErrRecovery
		}
		accountHeld, err := balance(tx, accountKey(scope.Account))
		if err != nil {
			return err
		}
		userHeld, err := balance(tx, ownerKey(scope.Account, scope.User))
		if err != nil {
			return err
		}
		if !within(accountHeld, a.Limit) || !within(userHeld, u.Limit) {
			return ErrCorrupt
		}
		accountFree, userFree := sub(a.Limit, accountHeld), sub(u.Limit, userHeld)
		if accountFree.Messages == 0 || userFree.Messages == 0 {
			return ErrLimit
		}
		available := min(accountFree.Bytes, userFree.Bytes)
		if available < floor {
			return ErrLimit
		}
		low, high := minimum, maximum
		for low < high {
			mid := low + (high-low+1)/2
			n, e := size(mid)
			if e != nil || n < floor || n > ceiling {
				return ErrLimit
			}
			if n <= available {
				low = mid
			} else {
				high = mid - 1
			}
		}
		encoded, e := size(low)
		if e != nil || encoded < floor || encoded > available || encoded > ceiling {
			return ErrLimit
		}
		quantum = low
		return s.issueTx(tx, scope, pool.Amount{Bytes: encoded, Messages: 1}, false, &grant)
	})
	if err != nil {
		return nil, 0, err
	}
	return &LocalTicket{store: s, grant: grant, folder: folder, MaxEncoded: grant.Limit.Bytes}, quantum, nil
}

// Key identifies a LOCAL grant. It is not an offer, remote usage report or
// permission for a client wallet to spend this grant.
func (t *LocalTicket) Key() wire.QuotaKey {
	g := t.grant
	return wire.QuotaKey{Store: g.Ref.Store, Grant: g.Ref.ID, Epoch: g.Run, Account: g.Ref.Scope.Account, User: g.Ref.Scope.User, Session: g.Ref.Scope.Session}
}

// Commit grants exactly one writer permission only after the complete body's
// charge/hash and grant close have committed atomically. An error authorizes
// NO I/O, including a lost commit acknowledgement. Its debt stays conservative.
func (t *LocalTicket) Commit(batch wire.ID, encoded uint64, decoded []byte) (Attempt, error) {
	if t == nil || t.store == nil || !validAttemptBody(batch, encoded, decoded) || encoded > t.MaxEncoded {
		return Attempt{}, ErrLimit
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.consumed {
		return Attempt{}, ErrState
	}
	t.consumed = true
	var attempt Attempt
	digest := sha256.Sum256(decoded)
	err := t.store.write(func(tx *bolt.Tx) error {
		if err := t.store.prepareTx(tx, t.grant.Ref, t.folder, batch, encoded, uint64(len(decoded)), digest, true, &attempt); err != nil {
			return err
		}
		return t.store.closeGrantTx(tx, t.grant.Ref)
	})
	if err != nil {
		return Attempt{}, err
	}
	return attempt, nil
}

func (t *LocalTicket) Cancel() error {
	if t == nil || t.store == nil {
		return ErrScope
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.consumed = true
	return t.store.CloseGrant(t.grant.Ref)
}
