package tunnel

import (
	"errors"
	"time"

	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/wallet"
	"itom.local/core/wire"
)

// Recovery fast batches are bounded to four <=8 KiB DATA records plus
// authenticated envelope/MIME overhead. Keep space for a following fast grant
// instead of tying up a full maximum bulk BODY for a few short requests.
const recoveryFastLeaseBytes = 128 << 10

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
	fastActive, bulkActive := l.issuedGrantCounts()
	// Base the short-traffic target on the directional share, not the shrinking
	// remainder after existing bulk leases. Repeated refills must not ratchet
	// a healthy multi-message grant down to one message while bulk stays open.
	fastMessages := min(uint64(fastLeaseMessages), max(uint64(1), (remoteShare.Messages-usage.HistoricalRemote.Messages)/5))
	if fast {
		available := min(profile.Messages-held.Messages-reserveMessages, remoteFree.Messages)
		if available == 0 || remoteFree.Bytes < recoveryFastLeaseBytes {
			return pool.Amount{}, journal.ErrLimit
		}
		// Existing bulk leases already provide their ordinals. At bootstrap keep
		// at least one slot for each remaining class whenever capacity permits.
		bulkReserve := min(uint64(max(0, remoteBulkLeases-bulkActive)), available-1)
		messages := min(fastMessages, available-bulkReserve)
		return pool.Amount{Bytes: recoveryFastLeaseBytes, Messages: messages}, nil
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
	// Keep an unissued short-traffic refill cushion in both finite resources.
	// This is a partition of genuinely unused quota, never a release of spent
	// attempts. Preserve at least one complete BODY per remaining bulk lease.
	if availableMessages > remaining {
		availableMessages -= min(uint64(1), availableMessages-remaining)
	}
	if availableBytes > remaining*dataBatchMIMEHardCap {
		availableBytes -= min(uint64(recoveryFastLeaseBytes), availableBytes-remaining*dataBatchMIMEHardCap)
	}
	remaining = min(remaining, availableMessages, availableBytes/dataBatchMIMEHardCap)
	if remaining == 0 {
		return pool.Amount{}, journal.ErrLimit
	}
	byteLimit := min(uint64(dataLeaseBytes), availableBytes/remaining)
	// Whole maximum-BODY units avoid trapping nearly a full copy of unused
	// permission in each small recovery lease. The next lease can use it.
	byteLimit -= byteLimit % dataBatchMIMEHardCap
	limit := pool.Amount{Bytes: byteLimit, Messages: min(uint64(dataLeaseMessages), availableMessages/remaining)}
	// WRITE_GRANT's historical fast marker is exactly 512 KiB / 32 slots.
	// A reduced bulk lease must never alias it and fill the peer's fast queue.
	if limit.Bytes == fastLeaseBytes && limit.Messages == fastLeaseMessages {
		limit.Messages--
	}
	if limit.Bytes < dataBatchMIMEHardCap || limit.Messages == 0 {
		return pool.Amount{}, journal.ErrLimit
	}
	return limit, nil
}

// A missing wallet cannot answer historical debt. Keep reconciling without
// continuously competing with live OPEN/DATA/echo controls. Progress reopens
// the short retry cadence; losing a reply still retries within five minutes.
type grantRecoveryRetry struct {
	sent                       time.Time
	attempts                   uint8
	used, registered, released pool.Amount
}

func (r grantRecoveryRetry) matches(g journal.Grant) bool {
	return r.used == g.Used && r.registered == g.Registered && r.released == g.Released
}
func (r grantRecoveryRetry) due(g journal.Grant, now time.Time, floor time.Duration) bool {
	if r.sent.IsZero() || !r.matches(g) {
		return true
	}
	delay := min(5*time.Minute, 15*time.Second*time.Duration(1<<min(r.attempts-1, uint8(5))))
	return now.Sub(r.sent) >= max(floor, delay)
}
func (r grantRecoveryRetry) sentFor(g journal.Grant, now time.Time) grantRecoveryRetry {
	attempts := uint8(1)
	if !r.sent.IsZero() && r.matches(g) {
		attempts = min(r.attempts+1, uint8(6))
	}
	return grantRecoveryRetry{sent: now, attempts: attempts, used: g.Used, registered: g.Registered, released: g.Released}
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
		l.grantRecoverySent = make(map[wire.QuotaKey]grantRecoveryRetry)
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
		if queued >= 4 || !l.grantRecoverySent[key].due(g, now, interval) {
			continue
		}
		offer := wire.StorageGrant{Key: key, Limit: wire.Quota{Bytes: g.Limit.Bytes, Messages: g.Limit.Messages}}
		if err = l.controlSend(wire.GrantRecovery{Offer: offer}); err != nil {
			return err
		}
		l.grantRecoverySent[key] = l.grantRecoverySent[key].sentFor(g, now)
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
