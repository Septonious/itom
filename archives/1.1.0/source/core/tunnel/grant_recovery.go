package tunnel

import (
	"errors"
	"sort"
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
	usage, err := l.data.UsageByOrigin(l.account.ID, l.user.ID, l.cipher.Context().Session)
	if err != nil {
		return pool.Amount{}, err
	}
	historical := pool.Amount{Bytes: usage.HistoricalLocal.Bytes + usage.HistoricalRemote.Bytes, Messages: usage.HistoricalLocal.Messages + usage.HistoricalRemote.Messages}
	retained := usage.HistoricalRemote.Bytes > 0 || usage.HistoricalRemote.Messages > 0
	historicalMessages := historical.Messages
	_, remoteShare, err := recoveredDirectionalShares(profile, usage)
	if err != nil {
		return pool.Amount{}, err
	}
	if usage.Remote.Bytes > remoteShare.Bytes || usage.Remote.Messages > remoteShare.Messages {
		return pool.Amount{}, journal.ErrLimit
	}
	remoteFree := pool.Amount{Bytes: remoteShare.Bytes - usage.Remote.Bytes, Messages: remoteShare.Messages - usage.Remote.Messages}
	fastBytes := recoveredFastReserveBytes(profile, usage)
	bulkBody := recoveredBulkBodyBytes(profile, usage)
	reserveBytes := fastBytes + bulkBody
	fastActive, bulkActive := l.issuedGrantCounts()
	neededBytes := bulkBody
	neededBulkBytes, neededBulkMessages := uint64(0), uint64(0)
	if fast {
		if bulkActive == 0 && !l.everReady.Load() {
			neededBulkBytes, neededBulkMessages = bulkBody, 1
		}
		// A usable bulk lease already occupies its share of held/remoteFree.
		// Reserve additional bulk headroom only during initial bootstrap.
		// After activation a fenced bulk BODY may await proof cleanup; its
		// still-charged bytes must not prevent independent fast refill.
		neededBytes = fastBytes + neededBulkBytes
	}
	if historicalMessages > profile.Messages {
		return pool.Amount{}, journal.ErrLimit
	}
	// Two local ordinals always protect fast + bulk. When historical debt
	// leaves only a few ordinals, share the remainder with the remote classes
	// instead of reserving all twelve free slots of a 180/192 field account.
	reserveMessages := min(max(uint64(2), profile.Messages/16), max(uint64(2), (profile.Messages-historicalMessages)/2))
	if !retained || held.Bytes > profile.Bytes || held.Messages > profile.Messages ||
		profile.Bytes-held.Bytes < reserveBytes+neededBytes ||
		profile.Messages-held.Messages <= reserveMessages {
		return pool.Amount{}, journal.ErrLimit
	}
	if fast {
		available := min(profile.Messages-held.Messages-reserveMessages, remoteFree.Messages)
		// Leave at least one bulk ordinal. When the standard fast grant cannot
		// fit alongside bulk, share the genuinely free message slots between
		// the two classes instead of requiring 32 unused slots to connect.
		if available < 1+neededBulkMessages || remoteFree.Bytes < fastBytes+neededBulkBytes {
			return pool.Amount{}, journal.ErrLimit
		}
		// Match the normal quota proportions instead of giving the half-MiB
		// latency lease most of a debt-bound account's remaining ordinals.
		messages := min(uint64(fastLeaseMessages), max(uint64(1), available/5))
		return pool.Amount{Bytes: fastBytes, Messages: messages}, nil
	}
	if fastActive == 0 {
		return pool.Amount{}, journal.ErrLimit
	}
	// Lease several complete physical copies when the charged ledger allows
	// it. A fixed one-BODY recovery lease made every copy wait for another
	// fence/GC/offer round trip, despite MiBs of genuinely unused capacity.
	remaining := uint64(max(1, remoteBulkLeases-bulkActive))
	availableBytes := min(profile.Bytes-held.Bytes-reserveBytes, remoteFree.Bytes)
	availableMessages := min(profile.Messages-held.Messages-reserveMessages, remoteFree.Messages)
	remaining = min(remaining, availableMessages, availableBytes/bulkBody)
	if remaining == 0 {
		return pool.Amount{}, journal.ErrLimit
	}
	byteLimit := min(uint64(dataLeaseBytes), availableBytes/remaining)
	// Whole maximum-BODY units avoid trapping nearly a full copy of unused
	// permission in each small recovery lease. The next lease can use it.
	byteLimit -= byteLimit % bulkBody
	limit := pool.Amount{Bytes: byteLimit, Messages: min(uint64(dataLeaseMessages), availableMessages/remaining)}
	// WRITE_GRANT's historical fast marker is exactly 512 KiB / 32 slots.
	// A reduced bulk lease must never alias it and fill the peer's fast queue.
	if limit.Bytes == fastLeaseBytes && limit.Messages == fastLeaseMessages {
		limit.Messages--
	}
	if limit.Bytes < bulkBody || limit.Messages == 0 {
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
	return l.reconcileUnpublishedGrantsAt(time.Now())
}

func (l *lane) reconcileUnpublishedGrantsAt(now time.Time) error {
	if now.Before(l.nextGrantRecovery) {
		return nil
	}
	grants, err := l.data.ScanGrants(0, 256)
	if err != nil {
		return err
	}
	if l.grantRecoverySent == nil {
		l.grantRecoverySent = make(map[wire.QuotaKey]time.Time)
	}
	interval := 15 * time.Second
	if l.stripe != nil {
		mode := l.stripe.deliverySnapshot().Mode
		if mode == string(deliveryCongested) || mode == string(deliveryOutage) {
			interval = 45 * time.Second
		}
	}
	// The per-key retry interval alone allowed a fresh batch on every offer()
	// call: hundreds of missing-wallet grants could monopolize control mail.
	// Fairly rotate the oldest audit first and cap the entire lane to four
	// per interval. Timing never closes or refunds an old reservation.
	keyOf := func(g journal.Grant) wire.QuotaKey {
		return wire.QuotaKey{Store: g.Ref.Store, Epoch: g.Run, Grant: g.Ref.ID, Session: g.Ref.Scope.Session, User: g.Ref.Scope.User, Account: g.Ref.Scope.Account}
	}
	sort.SliceStable(grants, func(i, j int) bool {
		return l.grantRecoverySent[keyOf(grants[i])].Before(l.grantRecoverySent[keyOf(grants[j])])
	})
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
	if queued > 0 {
		l.nextGrantRecovery = now.Add(interval)
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
			l.diag.mu.Lock()
			l.diag.line.MissingWalletRecoveryOffers++
			l.diag.mu.Unlock()
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
