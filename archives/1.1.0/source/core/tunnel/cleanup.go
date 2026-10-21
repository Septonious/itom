package tunnel

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/wire"
)

type cleanupKind uint8

const (
	cleanupRemote cleanupKind = iota
	cleanupLocalData
	cleanupLocalControl
	cleanupPresence
)

const (
	runtimeCleanupAttempts   = 32
	runtimePresenceUIDs      = 128
	runtimeCleanupPages      = 2
	runtimeCleanupCacheBytes = 16 << 20
)

func (l *lane) ownerCleanupPressure(kind cleanupKind) bool {
	switch kind {
	case cleanupRemote:
		// Small mail needs an ordinal bound; large BODYs exhaust a2MiB byte
		// lease after only a few APPENDs, long before16 ordinals. Reclaim after
		// half a bulk byte lease even when an unowned sibling is still ready.
		// This is scheduling advice only; existing exact GC proofs refund debt.
		if len(l.remoteGC) >= 16 {
			return true
		}
		bytes := 0
		for _, entry := range l.remoteGC {
			bytes += len(entry.message.MIME)
			if bytes >= dataLeaseBytes/2 {
				return true
			}
		}
		return false
	case cleanupPresence:
		// Begin reclaim well before the larger fail-closed locator hard limit.
		return len(l.known) >= 256
	case cleanupLocalData:
		if l.data == nil {
			return false
		}
		h, _, err := l.data.HeldByOrigin(l.account.ID, l.user.ID)
		// Incoming lease reservations are not local cleanup pressure. Reclaim
		// actual outgoing debt before the duplex share runs out of batches.
		if err != nil {
			return false
		}
		threshold := uint64(1 << 20)
		if h.Bytes > 0 {
			threshold = l.localCleanupThreshold()
		}
		return h.Bytes >= threshold || h.Messages >= 8
	case cleanupLocalControl:
		if l.control == nil {
			return false
		}
		h, err := l.control.Held(l.account.ID, l.user.ID)
		// writeControls starts throttling normal control at 80 messages.
		return err == nil && h.Messages >= 48
	default:
		return false
	}
}

// Schedule proof work before a debt-reduced outbound window fills. Normal
// profiles keep their established threshold; no timeout/pressure is a refund.
func (l *lane) localCleanupThreshold() uint64 {
	const threshold = uint64(1 << 20)
	if !l.side || l.stripe == nil || l.cipher == nil {
		return threshold
	}
	u, err := l.data.UsageByOrigin(l.account.ID, l.user.ID, l.cipher.Context().Session)
	if err != nil {
		return threshold
	}
	if u.HistoricalLocal.Bytes == 0 && u.HistoricalRemote.Bytes == 0 && u.HistoricalLocal.Messages == 0 && u.HistoricalRemote.Messages == 0 {
		return threshold
	}
	profile, err := l.data.Limit(l.account.ID, l.user.ID)
	if err != nil {
		return threshold
	}
	share, _, err := recoveredDirectionalShares(profile, u)
	if err != nil {
		return threshold
	}
	divisor := uint64(4)
	if l.stripe.delivery.modelEnabled {
		divisor = 16
	}
	window := share.Bytes - u.HistoricalLocal.Bytes
	if window <= localFastReserveBytes {
		return 64 << 10
	}
	return min(threshold, max(uint64(64<<10), (window-localFastReserveBytes)/divisor))
}

// pressuredLocalCleanup returns the local maintenance class that must be
// serviced before remote reclamation. Under sustained striped traffic the
// remote-GC queue can remain permanently non-empty; without this priority,
// remote cleanup can monopolize the single cleanup slot long enough for the
// durable control journal to hit writeControls' hard 112-message stop.
func (l *lane) pressuredLocalCleanup() (cleanupKind, bool) {
	kinds := [...]cleanupKind{cleanupLocalControl, cleanupLocalData, cleanupPresence}
	// Gate13 fixed remote-GC starvation by prioritising local pressure, but the
	// local priority itself was fixed control->data->presence. Under a speedtest
	// control and DATA can stay pressured forever, starving presence proof until
	// the authenticated receipt-locator window fills. Round-robin only among
	// pressured classes so every bounded correctness queue gets service.
	start := int(l.gcPressurePhase % uint8(len(kinds)))
	for i := 0; i < len(kinds); i++ {
		idx := (start + i) % len(kinds)
		kind := kinds[idx]
		if l.ownerCleanupPressure(kind) {
			l.gcPressurePhase = uint8((idx + 1) % len(kinds))
			return kind, true
		}
	}
	return 0, false
}

type cleanupResult struct {
	kind                 cleanupKind
	local                []journal.Attempt
	remote               []journal.Attempt
	checked              map[uint64]mailbox.Message
	present              map[uint32]bool
	presentCounters      map[uint64]bool
	err                  error
	elapsed              time.Duration
	remoteRecoveryCursor mailbox.Cursor
}

// deferOwnerCleanup keeps maintenance off the control-path owner while striped
// DATA still has a ready non-owner carrier. Remote cleanup is the exception when
// every ready carrier owns a stream: deferring it everywhere can exhaust all
// finite grants before any lane reaches the ordinary per-owner pressure threshold.
func (l *lane) deferOwnerCleanup(kind cleanupKind) bool {
	// Once a local pass has yielded its turn, service pending remote proof
	// even on an owner. Deferring that mandatory turn would also suppress all
	// later local passes and wedge the control journal at its hard stop.
	if kind == cleanupRemote && l.gcPreferRemote && len(l.remoteGC) > 0 {
		return false
	}
	if l.stripe == nil || !l.stripe.ownsActiveStream(l) || l.ownerCleanupPressure(kind) {
		return false
	}
	return kind != cleanupRemote || l.stripe.hasUnownedReadyCarrier()
}

// startCleanup snapshots exactly one maintenance class. Runtime cleanup is
// intentionally bounded: one pass can reclaim at most one physical 32-message
// batch, or perform one bounded presence proof. This prevents a burst of
// DELETE/FETCH/STORE/EXPUNGE commands from blocking every latency path while
// keeping all deletion/refund proofs unchanged.
func (l *lane) startCleanup(kind cleanupKind) bool {
	if l.cleanupQueued {
		kind = l.cleanupQueuedKind
	}
	if !l.cleanupQueued && l.deferOwnerCleanup(kind) {
		return false
	}
	if l.gcBusy {
		return false
	}
	releaseCleanupIO := false
	// Presence is read-only on the independent cleaner connection. It cannot
	// delete/refund DATA and must not reserve a destructive cleanup slot or
	// exclude short APPEND while proving that old receipt locators disappeared.
	// gcBusy still bounds this lane to one maintenance worker.
	if kind != cleanupPresence {
		if l.stripe != nil && !l.stripe.beginCleanup(l) {
			l.cleanupQueued = true
			l.cleanupQueuedKind = kind
			return false
		}
		l.cleanupWanted.Store(true)
		if l.stripe != nil {
			l.stripe.signal()
		}
		if !l.tryBeginCleanupIO() {
			l.cleanupQueued = true
			l.cleanupQueuedKind = kind
			return false
		}
		releaseCleanupIO = true
		l.gcActive.Store(true)
	}
	l.cleanupQueued = false
	l.cleanupWanted.Store(false)
	releaseGuard := func() {
		if releaseCleanupIO {
			l.endCleanupIO()
			if l.stripe != nil {
				l.stripe.endCleanup(l)
			}
			releaseCleanupIO = false
		}
	}
	eligible := map[journal.AttemptRef]bool{}
	remote := map[journal.AttemptRef]remoteCleanup{}
	known := map[uint64]mailbox.Message{}
	var localStore *journal.Store
	scan := false

	switch kind {
	case cleanupRemote:
		for ref, entry := range l.remoteGC {
			remote[ref] = entry
			if len(remote) >= runtimeCleanupAttempts {
				break
			}
		}
		if len(remote) == 0 {
			l.gcActive.Store(false)
			releaseGuard()
			return false
		}
	case cleanupLocalData, cleanupLocalControl:
		if kind == cleanupLocalData {
			localStore = l.data
		} else {
			localStore = l.control
		}
		storeID := localStore.StoreID()
		l.mu.Lock()
		for ref := range l.obsolete {
			if ref.Grant.Store != storeID {
				continue
			}
			eligible[ref] = true

		}
		l.mu.Unlock()
		// Even with no newly-obsolete refs, a bounded scan is useful for stale
		// attempts inherited from a prior logical session in the same process.
		scan = true
	case cleanupPresence:
		counters := make([]uint64, 0, len(l.known))
		for counter := range l.known {
			counters = append(counters, counter)
		}
		sort.Slice(counters, func(i, j int) bool { return counters[i] < counters[j] })
		// MailReceipt acknowledges the oldest 512 locators first. Probe those same
		// oldest locators for physical deletion so the acknowledgement window moves
		// forward instead of spending bounded cleanup on random newer map entries.
		type physicalLocation struct {
			folder   string
			location journal.Location
		}
		selected := map[physicalLocation]bool{}
		for _, counter := range counters {
			message := l.known[counter]
			location := physicalLocation{message.Folder, journal.Location{UIDValidity: message.Validity, UID: message.UID}}
			if !selected[location] {
				if len(selected) >= runtimePresenceUIDs {
					break
				}
				selected[location] = true
			}
			known[counter] = message
		}
		if len(known) == 0 {
			l.gcActive.Store(false)
			releaseGuard()
			return false
		}
	default:
		l.gcActive.Store(false)
		releaseGuard()
		return false
	}

	l.gcBusy = true
	l.gcPreferRemote = kind != cleanupRemote
	recoveryCursor := l.remoteRecoveryCursor
	l.workers.Add(1)
	go func() {
		defer l.workers.Done()
		defer releaseGuard()
		started := time.Now()
		result := l.cleanupRemoteAt(kind, eligible, remote, known, scan, localStore, recoveryCursor)
		// Destructive service has ended. A read-only receive audit may use the
		// same cleaner next, but cannot consume DATA maintenance admission.
		l.gcActive.Store(false)
		releaseGuard()
		result.kind = kind
		result.elapsed = time.Since(started)
		// End physical service before the actor can immediately schedule its next
		// pass. Publishing first could reuse and then clear the old reservation.
		releaseGuard()
		select {
		case l.gcDone <- result:
		case <-l.ctx.Done():
		}
	}()
	return true
}

func (l *lane) cleanup(kind cleanupKind, eligible map[journal.AttemptRef]bool, remote map[journal.AttemptRef]remoteCleanup, known map[uint64]mailbox.Message, scan bool, localStore *journal.Store) (r cleanupResult) {
	return l.cleanupRemoteAt(kind, eligible, remote, known, scan, localStore, mailbox.Cursor{})
}

func (l *lane) cleanupRemoteAt(kind cleanupKind, eligible map[journal.AttemptRef]bool, remote map[journal.AttemptRef]remoteCleanup, known map[uint64]mailbox.Message, scan bool, localStore *journal.Store, recoveryCursor mailbox.Cursor) (r cleanupResult) {
	r.kind = kind
	proofs := cleanupProofs{}
	defer proofs.clear()
	if kind == cleanupRemote {
		rows := make([]mailbox.Message, 0, len(remote))
		for _, m := range remote {
			if m.message.UID != 0 {
				rows = append(rows, m.message)
			}
		}
		recovered, next, err := l.discoverRecoveredRemote(remote, recoveryCursor)
		if err != nil {
			r.err = err
			return
		}
		r.remoteRecoveryCursor = next
		rows = append(rows, recovered...)
		r.remote, r.err = reclaimWithProofs(l.ctx, l.mail, l.data, rows, func(a journal.Attempt) bool {
			_, ok := remote[a.Ref]
			return ok
		}, runtimeCleanupAttempts, proofs)
		if r.err != nil {
			return
		}
		if len(r.remote) != len(rows) {
			r.err = errors.New("REMOTE_GC_INCOMPLETE")
		}
		return
	}

	if scan && localStore != nil {
		attempts, err := allAttempts(localStore)
		if err != nil {
			r.err = err
			return
		}
		currentSession := l.cipher.Context().Session
		folder := l.sendFolder
		for _, attempt := range attempts {
			if eligible[attempt.Ref] || attempt.Ordinal == 0 && attempt.Ref.Grant.Scope.Session != currentSession {
				for _, owned := range l.ownedFolders(localStore, true) {
					if attempt.Folder == owned {
						folder = owned
						break
					}
				}
				if folder != l.sendFolder {
					break
				}
			}
		}
		if l.outCursors == nil {
			l.outCursors = map[string]mailbox.Cursor{}
		}
		l.outCursor = l.outCursors[folder]
		defer func() { l.outCursors[folder] = l.outCursor }()
		hasCandidates := false
		for _, a := range attempts {
			if a.Folder == folder && (eligible[a.Ref] || a.Ordinal == 0 && a.Ref.Grant.Scope.Session != currentSession) {
				hasCandidates = true
				break
			}
		}
		// Peer-charged rows and our unacknowledged current-session attempts
		// cannot be reclaimed here. Avoid repeated payload FETCH/Decode until
		// the journal contains a candidate; this does not authorize a refund.
		if !hasCandidates {
			return
		}
		// The shared send folder also contains DATA charged to the peer's
		// journal. Retain MIME only for live attempts in our own stores; peer
		// cleanup can remove the other rows without ever returning a local proof.
		live := map[[32]byte]bool{}
		for _, store := range []*journal.Store{l.data, l.control} {
			if store == nil {
				continue
			}
			attempts, err := allAttempts(store)
			if err != nil {
				r.err = err
				return
			}
			for _, a := range attempts {
				if a.Folder == folder {
					live[a.Digest] = true
				}
			}
		}
		retain := func(rows []mailbox.Message) []mailbox.Message {
			kept := rows[:0]
			for _, row := range rows {
				proof := proofs.decode(row.MIME)
				if proof.err == nil && live[proof.digest] {
					kept = append(kept, row)
				}
			}
			clear(rows[len(kept):])
			return kept
		}
		l.outCache = retain(l.outCache)
		// Drain an already observed batch before reading more of the shared
		// folder. Re-reading two pages ahead of every delete can make eligible
		// control debt grow faster than the cleaner can service it. Only cached
		// digest-matched attempts enter this turn; physical deletion and journal
		// proof below remain mandatory, and the 32-attempt bound is unchanged.
		cachedDigests := map[[32]byte]bool{}
		for _, row := range l.outCache {
			proof := proofs.decode(row.MIME)
			if proof.err == nil {
				cachedDigests[proof.digest] = true
			}
		}
		cachedEligible := map[journal.AttemptRef]bool{}
		for _, a := range attempts {
			if eligible[a.Ref] && cachedDigests[a.Digest] {
				cachedEligible[a.Ref] = true
			}
		}
		cachedBatchMinimum := 8
		if kind == cleanupLocalData {
			// DATA holds fewer than eight full-size MIME messages. Fetch already
			// eligible peers before paying a whole delete/proof round trip for a
			// small cached batch (up to four rows are enough). Never wait for a
			// future acknowledgement: a sole eligible row can drain immediately.
			cachedBatchMinimum = min(4, max(1, len(eligible)))
		}
		drainCached := len(cachedEligible) >= cachedBatchMinimum
		if !drainCached {
			if finder, ok := l.mail.(interface {
				FetchCleanupCandidates(context.Context, string, map[[32]byte]bool) ([]mailbox.Message, error)
			}); ok {
				wanted := map[[32]byte]bool{}
				for _, a := range attempts {
					if eligible[a.Ref] && !cachedDigests[a.Digest] {
						wanted[a.Digest] = true
					}
				}
				if len(wanted) > 0 {
					rows, err := finder.FetchCleanupCandidates(l.ctx, folder, wanted)
					if err != nil {
						r.err = err
						return
					}
					rows = retain(rows)
					merged, err := mergeProofCache(l.outCache, rows)
					if err != nil {
						r.err = err
						return
					}
					l.outCache = merged
					for _, row := range rows {
						proof := proofs.decode(row.MIME)
						if proof.err == nil {
							cachedDigests[proof.digest] = true
						}
					}
					for _, a := range attempts {
						if eligible[a.Ref] && cachedDigests[a.Digest] {
							cachedEligible[a.Ref] = true
						}
					}
					// A bounded exact FETCH has already paid the discovery cost.
					// Delete its matched eligible rows before any full-folder scan.
					drainCached = len(cachedEligible) > 0
				}
			}
		}
		for pages := 0; !drainCached && pages < runtimeCleanupPages; pages++ {
			rows, next, err := l.mail.Page(l.ctx, folder, l.outCursor)
			if err != nil {
				r.err = err
				return
			}
			if l.outCursor.Validity != 0 && l.outCursor.Validity != next.Validity {
				clear(l.outCache)
				l.outCache = nil
			}
			pageRows := len(rows)
			rows = retain(rows)
			l.outCache, err = mergeProofCache(l.outCache, rows)
			if err != nil {
				r.err = err
				return
			}
			if next == l.outCursor {
				break
			}
			l.outCursor = next
			if pageRows < 16 {
				break
			}
		}
		gone, err := reclaimWithProofs(l.ctx, l.mail, localStore, l.outCache, func(a journal.Attempt) bool {
			if drainCached {
				return cachedEligible[a.Ref]
			}
			return eligible[a.Ref] || a.Ordinal == 0 && a.Ref.Grant.Scope.Session != currentSession
		}, runtimeCleanupAttempts, proofs)
		if err != nil {
			r.err = err
			return
		}
		r.local = append(r.local, gone...)
		hashes := make(map[[32]byte]bool, len(r.local))
		for _, a := range r.local {
			hashes[a.Digest] = true
		}
		retained := l.outCache[:0]
		for _, msg := range l.outCache {
			proof := proofs.decode(msg.MIME)
			if proof.err != nil || !hashes[proof.digest] {
				retained = append(retained, msg)
			}
		}
		clear(l.outCache[len(retained):])
		l.outCache = retained
		return
	}

	if kind != cleanupPresence {
		return
	}
	r.checked = known
	r.presentCounters = map[uint64]bool{}
	type groupKey struct {
		folder   string
		validity uint32
	}
	groups := map[groupKey]map[uint64]mailbox.Message{}
	for counter, m := range known {
		folder := m.Folder
		if folder == "" {
			folder = l.recvFolder
		}
		key := groupKey{folder, m.Validity}
		if groups[key] == nil {
			groups[key] = map[uint64]mailbox.Message{}
		}
		groups[key][counter] = m
	}
	for key, entries := range groups {
		ids := []uint32{}
		seen := map[uint32]bool{}
		for _, m := range entries {
			if !seen[m.UID] {
				ids = append(ids, m.UID)
				seen[m.UID] = true
			}
		}
		found := map[uint32]bool{}
		var err error
		if batch, ok := l.mail.(interface {
			Present(context.Context, string, uint32, []uint32) (map[uint32]bool, error)
		}); ok {
			found, err = batch.Present(l.ctx, key.folder, key.validity, ids)
		} else {
			for _, id := range ids {
				var absent bool
				absent, err = l.mail.Absent(l.ctx, key.folder, key.validity, id)
				if err != nil {
					break
				}
				if !absent {
					found[id] = true
				}
			}
		}
		if errors.Is(err, mailbox.ErrChanged) {
			for counter := range entries {
				r.presentCounters[counter] = true
			}
			continue
		}
		if err != nil {
			r.err = err
			return
		}
		for counter, m := range entries {
			r.presentCounters[counter] = found[m.UID]
		}
	}

	return
}

func (l *lane) finishCleanup(r cleanupResult) error {
	l.gcActive.Store(false)
	if r.kind == cleanupRemote && r.err == nil {
		l.remoteRecoveryCursor = r.remoteRecoveryCursor
	}
	if l.control != nil {
		if held, err := l.control.Held(l.account.ID, l.user.ID); err == nil {
			l.controlDebt.Store(held.Messages)
			bulkLimit := uint64(64)
			if l.side {
				bulkLimit = 80
			}
			l.bulkControlBlocked.Store(held.Messages >= bulkLimit)
		}
	}
	if l.stripe != nil {
		l.stripe.signal()
	}
	if l.diag != nil && l.diag.tracer != nil {
		// Attempt references and digests identify durable refunds, never mailbox labels.
		for _, a := range append(append([]journal.Attempt(nil), r.local...), r.remote...) {
			b, _ := json.Marshal(map[string]any{"ref": a.Ref, "digest": a.Digest})
			l.diag.trace(traceEvent{Event: "gc_proven", DurationUS: r.elapsed.Microseconds(), Details: b})
		}
		l.diag.trace(traceEvent{Event: "gc_finished", Code: errorCode(r.err), DurationUS: r.elapsed.Microseconds()})
	}

	workerErr := r.err
	type cleanedAttempt struct {
		owner *lane
		id    uint64
	}
	cleaned := []cleanedAttempt{}
	l.mu.Lock()
	for _, a := range r.local {
		delete(l.obsolete, a.Ref)
		for i := 0; i < len(l.out); i++ {
			if l.out[i].ref == a.Ref {
				for _, id := range l.out[i].attempts {
					cleaned = append(cleaned, cleanedAttempt{owner: l.out[i].owner, id: id})
				}
				l.out = append(l.out[:i], l.out[i+1:]...)
				break
			}
		}
	}
	l.refreshUnreceivedLocalLocked()
	l.mu.Unlock()
	for _, a := range r.local {
		if l.stripe == nil {
			continue
		}
		for _, entry := range l.stripe.takeLocal(l.account.ID, a.Ref) {
			duplicate := false
			for _, item := range cleaned {
				if item.owner == entry.owner && item.id == entry.id {
					duplicate = true
					break
				}
			}
			if !duplicate {
				cleaned = append(cleaned, cleanedAttempt{owner: entry.owner, id: entry.id})
			}
		}
	}
	for _, item := range cleaned {
		if err := l.cleanedFor(item.owner, item.id); err != nil {
			return err
		}
	}
	published := map[wire.QuotaKey]bool{}
	for _, a := range r.remote {
		entry := l.remoteGC[a.Ref]
		delete(l.remoteGC, a.Ref)
		delete(l.reports, entry.digest)
		delete(l.pending, entry.digest)
		if published[entry.key] {
			continue
		}
		status, err := l.data.RemoteStatus(grantRef(entry.key))
		if errors.Is(err, journal.ErrMissing) {
			continue
		}
		if err != nil {
			return err
		}
		if err = l.publishCleanup(status); err != nil {
			return err
		}
		published[entry.key] = true
	}
	// Only a complete presence proof may prune counters. A transient cleaner
	// failure can still carry valid durable deletion proofs in r.local/r.remote,
	// but absence is not established until the entire bounded query succeeds.
	if workerErr == nil {
		pruned := false
		for counter, m := range r.checked {
			present := r.present[m.UID]
			if r.presentCounters != nil {
				present = r.presentCounters[counter]
			}
			if !present {
				if current, ok := l.known[counter]; ok && current.UID == m.UID && current.Validity == m.Validity && current.Folder == m.Folder {
					delete(l.known, counter)
					pruned = true
				}
			}
		}
		// If a >512 locator backlog exists, pruning the oldest acknowledged
		// entries exposes the next ITH9 receipt window. Mark it dirty so normal
		// maintenance publishes that next bounded window without waiting five
		// seconds for the periodic receipt refresh.
		if pruned && len(l.known) > 0 {
			l.ackDirty = true
		}
	}
	if l.diag != nil {
		l.diag.mu.Lock()
		l.diag.line.CleanupMS = r.elapsed.Milliseconds()
		l.diag.line.CleanupMessages += int64(len(r.local) + len(r.remote))
		switch r.kind {
		case cleanupRemote:
			l.diag.line.CleanupRemoteRuns++
			l.diag.line.CleanupRemoteMessages += int64(len(r.remote))
		case cleanupLocalData:
			l.diag.line.CleanupLocalDataRuns++
			l.diag.line.CleanupLocalDataMessages += int64(len(r.local))
		case cleanupLocalControl:
			l.diag.line.CleanupLocalControlRuns++
			l.diag.line.CleanupLocalControlMessages += int64(len(r.local))
		case cleanupPresence:
			l.diag.line.CleanupPresenceRuns++
		}
		l.diag.mu.Unlock()
	}
	if l.side && l.pump != nil && len(r.local) > 0 {
		// Local physical cleanup may be the event that re-opens enough of the
		// shared 8 MiB DATA journal for the third inbound cumulative lease. Refill
		// immediately instead of waiting for the one-second maintenance tick.
		if err := l.offer(); err != nil {
			return err
		}
	}
	if workerErr != nil {
		return workerErr
	}
	return nil
}

// Shared DATA ledgers include issued inbound leases. Their held amount may
// remain pressured even when there are no reclaimable local messages. Give
// pending remote proof a turn after each local pass so refill cannot starve.
func (l *lane) remoteCleanupTurn(now time.Time) bool {
	return l.gcPreferRemote && len(l.remoteGC) > 0 && !now.Before(l.nextRemoteCleanup)
}

// The cache holds observed BODY evidence, not APPEND hints. Repeated UID reads
// do not consume the bounded byte budget twice. A changed body at the same
// physical identity is a protocol error; no deletion/refund is authorized here.
func mergeProofCache(existing, rows []mailbox.Message) ([]mailbox.Message, error) {
	type identity struct {
		folder        string
		validity, uid uint32
	}
	type proof struct {
		digest [32]byte
		size   int
	}
	seen := map[identity]proof{}
	result := make([]mailbox.Message, 0, len(existing)+len(rows))
	size := 0
	for _, batch := range [][]mailbox.Message{existing, rows} {
		for _, row := range batch {
			body, err := mailbox.Decode(row.MIME)
			if err != nil {
				return nil, err
			}
			id := identity{row.Folder, row.Validity, row.UID}
			digest := sha256.Sum256(body)
			if old, ok := seen[id]; ok {
				if old.digest != digest || old.size != len(row.MIME) {
					return nil, mailbox.ErrChanged
				}
				continue
			}
			seen[id] = proof{digest, len(row.MIME)}
			size += len(row.MIME)
			if size > runtimeCleanupCacheBytes || len(result) >= 512 {
				return nil, mailbox.ErrLimit
			}
			result = append(result, row)
		}
	}
	return result, nil
}
