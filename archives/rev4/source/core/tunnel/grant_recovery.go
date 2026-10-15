package tunnel

import (
	"errors"
	"time"

	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/wallet"
	"itom.local/core/wire"
)

// Called under localGrantMu. Historical debt may predate the current directional
// shares and is not a live grant for this carrier. Keep every byte/message
// charged, while leasing a full legal BODY from genuinely unused capacity.
// Reduced fast leases carry an authenticated explicit class. Normal, debt-free
// admission and the physical profile limits stay unchanged.
func (l *lane) recoveryQuotaLocked(profile, held pool.Amount, fast bool) (pool.Amount, error) {
	if l.cipher == nil || !l.side {
		return pool.Amount{}, journal.ErrLimit
	}
	grants, err := l.data.ScanGrants(0, 256)
	if err != nil {
		return pool.Amount{}, err
	}
	retained := false
	for _, g := range grants {
		if !g.Remote || g.Ref.Scope.Session == l.cipher.Context().Session {
			continue
		}
		charge := g.Limit
		if g.Closed {
			charge = g.Used
		}
		if charge.Bytes > g.Released.Bytes || charge.Messages > g.Released.Messages {
			retained = true
			break
		}
	}
	reserveBytes := uint64(localFastReserveBytes + dataBatchMIMEHardCap)
	reserveMessages := max(uint64(2), profile.Messages/16)
	if !retained || held.Bytes > profile.Bytes || held.Messages > profile.Messages ||
		profile.Bytes-held.Bytes < reserveBytes+dataBatchMIMEHardCap ||
		profile.Messages-held.Messages <= reserveMessages {
		return pool.Amount{}, journal.ErrLimit
	}
	if fast {
		available := profile.Messages - held.Messages - reserveMessages
		// Leave at least one bulk ordinal. When the standard fast grant cannot
		// fit alongside bulk, share the genuinely free message slots between
		// the two classes instead of requiring 32 unused slots to connect.
		if available < 2 {
			return pool.Amount{}, journal.ErrLimit
		}
		messages := uint64(fastLeaseMessages)
		if available <= fastLeaseMessages {
			messages = available / 2
		}
		return pool.Amount{Bytes: fastLeaseBytes, Messages: messages}, nil
	}
	if active, _ := l.issuedGrantCounts(); active == 0 {
		return pool.Amount{}, journal.ErrLimit
	}
	return pool.Amount{Bytes: dataBatchMIMEHardCap, Messages: min(uint64(dataLeaseMessages/2), profile.Messages-held.Messages-reserveMessages)}, nil
}

// Reconcile all open old reservations, including partially spent grants. A lost
// publication or final fence can otherwise permanently occupy message slots.
// These controls describe immutable
// journal allocations; they do not release a byte or authorize old-session DATA.
func (l *lane) reconcileUnpublishedGrants() error {
	if !l.side || l.cipher == nil || l.boot == nil || !l.boot.Ready() {
		return nil
	}
	grants, err := l.data.ScanGrants(0, 256)
	if err != nil {
		return err
	}
	if l.grantRecoverySent == nil {
		l.grantRecoverySent = make(map[wire.QuotaKey]time.Time)
	}
	now := time.Now()
	interval := 15 * time.Second
	if l.stripe != nil {
		mode := l.stripe.deliverySnapshot().Mode
		if mode == string(deliveryCongested) || mode == string(deliveryOutage) {
			interval = 45 * time.Second
		}
	}
	queued := 0
	live := make(map[wire.QuotaKey]bool)
	for _, g := range grants {
		if !g.Remote || g.Closed || g.Ref.Scope.Session == l.cipher.Context().Session {
			continue
		}
		key := wire.QuotaKey{Store: g.Ref.Store, Epoch: g.Run, Grant: g.Ref.ID, Session: g.Ref.Scope.Session, User: g.Ref.Scope.User, Account: g.Ref.Scope.Account}
		if key.Account != l.account.ID || key.User != l.user.ID {
			return journal.ErrScope
		}
		live[key] = true
		if queued >= 4 || now.Sub(l.grantRecoverySent[key]) < interval {
			continue
		}
		offer := wire.StorageGrant{Key: key, Limit: wire.Quota{Bytes: g.Limit.Bytes, Messages: g.Limit.Messages}}
		if err = l.controlSend(wire.GrantRecovery{Offer: offer}); err != nil {
			return err
		}
		l.grantRecoverySent[key] = now
		queued++
	}
	for key := range l.grantRecoverySent {
		if !live[key] {
			delete(l.grantRecoverySent, key)
		}
	}
	return nil
}

func (l *lane) reconcileGrantOffer(offer wire.StorageGrant) error {
	if l.side || l.wallet == nil || offer.Key.Account != l.account.ID || offer.Key.User != l.user.ID || offer.Key.Session == l.cipher.Context().Session {
		return journal.ErrScope
	}
	entry, err := l.wallet.ReconcileOffer(offer)
	if errors.Is(err, wallet.ErrMissing) {
		if l.diag != nil {
			l.diag.event("event=grant_recovery_deferred code=WALLET_HISTORY_MISSING")
		}
		return nil // lost client history is never substituted for a zero-spend proof
	}
	if err != nil {
		return err
	}
	for _, report := range entry.Pending {
		if err = l.controlSend(report); err != nil {
			return err
		}
	}
	if entry.Retired {
		return l.sendRetirement(wire.Retirement{Key: offer.Key, Final: entry.Used})
	}
	return l.controlSend(wire.StorageRelease{Key: offer.Key, Final: entry.Used})
}
