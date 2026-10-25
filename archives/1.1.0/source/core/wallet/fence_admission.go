package wallet

import (
	"context"
	bolt "go.etcd.io/bbolt"
	"itom.local/core/wire"
)

func (w *Wallet) signalAdmissionLocked() {
	if w.admissionChanged == nil {
		return
	}
	close(w.admissionChanged)
	w.admissionChanged = make(chan struct{})
}

// FenceAfterAdmissions stops NEW reservations for one original grant, then
// waits for already-reserved tickets to commit/cancel before recording Final.
// It never invalidates a live writer's ticket, resets Used or creates recovery
// allowance. Cancellation removes only the volatile admission stop; any durable
// spend/fence already committed remains unchanged. The owner must cancel waits
// before closing this borrowed wallet.
func (w *Wallet) FenceAfterAdmissions(ctx context.Context, k wire.QuotaKey) (wire.StorageRelease, error) {
	w.mu.Lock()
	if w.failed {
		w.mu.Unlock()
		return wire.StorageRelease{}, ErrIO
	}
	var existing Entry
	err := w.db.View(func(tx *bolt.Tx) error { var e error; existing, e = get(tx.Bucket(grants), k); return e })
	if err != nil {
		w.mu.Unlock()
		return wire.StorageRelease{}, err
	}
	if existing.Fenced {
		w.mu.Unlock()
		return wire.StorageRelease{Key: k, Final: existing.Used}, nil
	}
	if k.Session != w.session || w.fencing[k] {
		w.mu.Unlock()
		return wire.StorageRelease{}, ErrState
	}
	if w.fencing == nil {
		w.fencing = make(map[wire.QuotaKey]bool)
	}
	w.fencing[k] = true
	if w.admissionChanged == nil {
		w.admissionChanged = make(chan struct{})
	}
	w.mu.Unlock()
	defer func() { w.mu.Lock(); delete(w.fencing, k); w.signalAdmissionLocked(); w.mu.Unlock() }()
	for {
		if err := ctx.Err(); err != nil {
			return wire.StorageRelease{}, err
		}
		w.mu.Lock()
		if w.failed {
			w.mu.Unlock()
			return wire.StorageRelease{}, ErrIO
		}
		_, pending := w.reservedLocked(k, 0)
		changed := w.admissionChanged
		w.mu.Unlock()
		if pending == 0 {
			return w.Fence(k)
		}
		select {
		case <-ctx.Done():
			return wire.StorageRelease{}, ctx.Err()
		case <-changed:
		}
	}
}
