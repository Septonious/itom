package wallet

import (
	"encoding/json"
	bolt "go.etcd.io/bbolt"
	"itom.local/core/wire"
)

type ticketReservation struct {
	key   wire.QuotaKey
	bytes uint64
}

// Ticket reserves one physical body on a concrete grant before batching and
// encryption. It is not a new grant or refund. Crash drops only unspent tickets;
// durable Used and pending physical descriptors survive unchanged.
type Ticket struct {
	wallet     *Wallet
	id         uint64
	Key        wire.QuotaKey
	MaxEncoded uint64
}

func (w *Wallet) reservedLocked(k wire.QuotaKey, exclude uint64) (bytes, messages uint64) {
	for id, t := range w.tickets {
		if id != exclude && t.key == k {
			bytes += t.bytes
			messages++
		}
	}
	return
}
func (w *Wallet) Reserve(encodedCeiling uint64) (*Ticket, error) {
	if encodedCeiling == 0 || encodedCeiling > 2<<20 {
		return nil, ErrLimit
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failed {
		return nil, ErrIO
	}
	if len(w.tickets) >= MaxPending {
		return nil, ErrLimit
	}
	var chosen wire.QuotaKey
	var remaining uint64 = ^uint64(0)
	err := w.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(grants).ForEach(func(k, v []byte) error {
			var e Entry
			if json.Unmarshal(v, &e) != nil {
				return ErrState
			}
			if e.Fenced || e.Retired || w.fencing[e.Offer.Key] || e.Offer.Key.Session != w.session {
				return nil
			}
			r, m := w.reservedLocked(e.Offer.Key, 0)
			if e.Used.Bytes > e.Offer.Limit.Bytes || e.Used.Messages > e.Offer.Limit.Messages {
				return ErrState
			}
			free := e.Offer.Limit.Bytes - e.Used.Bytes
			if r > free || m >= e.Offer.Limit.Messages-e.Used.Messages || len(e.Pending)+int(m) >= MaxPending {
				return nil
			}
			free -= r
			if free >= encodedCeiling && free < remaining {
				chosen = e.Offer.Key
				remaining = free
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	if remaining == ^uint64(0) {
		return nil, ErrExhausted
	}
	if w.nextTicket == ^uint64(0) {
		return nil, ErrLimit
	}
	w.nextTicket++
	if w.tickets == nil {
		w.tickets = make(map[uint64]ticketReservation)
	}
	w.tickets[w.nextTicket] = ticketReservation{key: chosen, bytes: encodedCeiling}
	return &Ticket{wallet: w, id: w.nextTicket, Key: chosen, MaxEncoded: encodedCeiling}, nil
}
func (t *Ticket) Cancel() {
	if t == nil || t.wallet == nil {
		return
	}
	w := t.wallet
	w.mu.Lock()
	if _, exists := w.tickets[t.id]; exists {
		delete(w.tickets, t.id)
		w.signalAdmissionLocked()
	}
	w.mu.Unlock()
}
func (t *Ticket) Commit(folder string, encrypted, authenticationKey []byte, encodedSize func([]byte) (uint64, error)) (wire.UsageReport, []byte, error) {
	if t == nil || t.wallet == nil || len(authenticationKey) != 32 || encodedSize == nil {
		return wire.UsageReport{}, nil, ErrState
	}
	return t.wallet.spend(t.Key, folder, encrypted, authenticationKey, encodedSize, t.id)
}

// Identity returns only public durable scope. It allows a new transport to
// reject a wallet/Noise-session mismatch before it spends any grant or nonce.
type Identity struct {
	User           string
	Store, Session wire.ID
}

func (w *Wallet) Identity() Identity {
	return Identity{User: w.user, Store: w.store, Session: w.session}
}
