package wallet

import (
	"encoding/json"
	bolt "go.etcd.io/bbolt"
	"itom.local/core/wire"
)

// ReserveSized selects a concrete account grant and fits a quantum to its
// remaining canonical physical allowance. size must be a pure, monotonic,
// nonblocking length function: it must not encrypt, allocate a nonce or do I/O.
// No unsuitable grant is fenced. All existing pending/message limits apply.
func (w *Wallet) ReserveSized(account string, minimum, maximum int, size func(wire.QuotaKey, int) (uint64, error)) (*Ticket, int, error) {
	if account == "" || minimum < 0 || maximum < minimum || maximum > 1<<20 || size == nil {
		return nil, 0, ErrLimit
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failed {
		return nil, 0, ErrIO
	}
	if len(w.tickets) >= MaxPending || w.nextTicket == ^uint64(0) {
		return nil, 0, ErrLimit
	}
	var key wire.QuotaKey
	var largest uint64
	found := false
	err := w.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(grants).ForEach(func(k, v []byte) error {
			var entry Entry
			if json.Unmarshal(v, &entry) != nil {
				return ErrState
			}
			if entry.Fenced || entry.Retired || entry.Offer.Key.Session != w.session || entry.Offer.Key.Account != account {
				return nil
			}
			if entry.Used.Bytes > entry.Offer.Limit.Bytes || entry.Used.Messages > entry.Offer.Limit.Messages {
				return ErrState
			}
			held, messages := w.reservedLocked(entry.Offer.Key, 0)
			free := entry.Offer.Limit.Bytes - entry.Used.Bytes
			if held > free || messages >= entry.Offer.Limit.Messages-entry.Used.Messages || len(entry.Pending)+int(messages) >= MaxPending {
				return nil
			}
			free -= held
			floor, e := size(entry.Offer.Key, minimum)
			if e != nil {
				return e
			}
			if floor == 0 || floor > 2<<20 {
				return ErrLimit
			}
			if free >= floor && (!found || free > largest) {
				key = entry.Offer.Key
				largest = free
				found = true
			}
			return nil
		})
	})
	if err != nil {
		return nil, 0, err
	}
	if !found {
		return nil, 0, ErrExhausted
	}
	low, high := minimum, maximum
	for low < high {
		mid := low + (high-low+1)/2
		n, e := size(key, mid)
		if e != nil {
			return nil, 0, e
		}
		if n == 0 || n > 2<<20 {
			return nil, 0, ErrLimit
		}
		if n <= largest {
			low = mid
		} else {
			high = mid - 1
		}
	}
	encoded, err := size(key, low)
	if err != nil {
		return nil, 0, err
	}
	if encoded == 0 || encoded > largest || encoded > 2<<20 {
		return nil, 0, ErrLimit
	}
	w.nextTicket++
	if w.tickets == nil {
		w.tickets = make(map[uint64]ticketReservation)
	}
	w.tickets[w.nextTicket] = ticketReservation{key: key, bytes: encoded}
	return &Ticket{wallet: w, id: w.nextTicket, Key: key, MaxEncoded: encoded}, low, nil
}
