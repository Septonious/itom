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
	retained := false
	historicalMessages := uint64(0)
	// Compute the reserve from historical exposure, not the shrinking free
	// balance after each new grant. Otherwise repeated refills can consume the
	// same local cushion. Pagination also finds retained debt after old zero-
	// charge entries; every physical and historical charge remains unchanged.
	for after := uint64(0); ; {
		grants, err := l.data.ScanGrants(after, 256)
		if err != nil {
			return pool.Amount{}, err
		}
		for _, g := range grants {
			if g.Ref.Scope.Account != l.account.ID || g.Ref.Scope.User != l.user.ID || g.Ref.Scope.Session == l.cipher.Context().Session {
				continue
			}
			charge := g.Limit
			if g.Closed {
				charge = g.Used
			}
			if charge.Messages < g.Released.Messages || charge.Bytes < g.Released.Bytes {
				return pool.Amount{}, journal.ErrCorrupt
			}
			messages := charge.Messages - g.Released.Messages
			if historicalMessages > profile.Messages || messages > profile.Messages-historicalMessages {
				return pool.Amount{}, journal.ErrLimit
			}
			historicalMessages += messages
			if g.Remote && (charge.Bytes > g.Released.Bytes || messages > 0) {
				retained = true
			}
		}
		if len(grants) < 256 {
			break
		}
		after = grants[len(grants)-1].Ref.ID
	}
	reserveBytes := uint64(localFastReserveBytes + dataBatchMIMEHardCap)
	if historicalMessages > profile.Messages {
		return pool.Amount{}, journal.ErrLimit
	}
	// Two local ordinals always protect fast + bulk. When historical debt
	// leaves only a few ordinals, share the remainder with the remote classes
	// instead of reserving all twelve free slots of a 180/192 field account.
	reserveMessages := min(max(uint64(2), profile.Messages/16), max(uint64(2), (profile.Messages-historicalMessages)/2))
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
		// Match the normal quota proportions instead of giving the half-MiB
		// latency lease most of a debt-bound account's remaining ordinals.
		messages := min(uint64(fastLeaseMessages), max(uint64(1), available/5))
		return pool.Amount{Bytes: fastLeaseBytes, Messages: messages}, nil
	}
	fastActive, bulkActive := l.issuedGrantCounts()
	if fastActive == 0 {
		return pool.Amount{}, journal.ErrLimit
	}
	// Lease several complete physical copies when the charged ledger allows
	// it. A fixed one-BODY recovery lease made every copy wait for another
	// fence/GC/offer round trip, despite MiBs of genuinely unused capacity.
	remaining := uint64(max(1, remoteBulkLeases-bulkActive))
	availableBytes := profile.Bytes - held.Bytes - reserveBytes
	availableMessages := profile.Messages - held.Messages - reserveMessages
	remaining = min(remaining, availableMessages, availableBytes/dataBatchMIMEHardCap)
	if remaining == 0 {
		return pool.Amount{}, journal.ErrLimit
	}
	limit := pool.Amount{Bytes: min(uint64(dataLeaseBytes), availableBytes/remaining), Messages: min(uint64(dataLeaseMessages), availableMessages/remaining)}
	if limit.Bytes < dataBatchMIMEHardCap || limit.Messages == 0 {
		return pool.Amount{}, journal.ErrLimit
	}
	return limit, nil
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
