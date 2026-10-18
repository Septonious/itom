package tunnel

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"itom.local/core/datapath"
	"itom.local/core/journal"
	"itom.local/core/socks"
	"itom.local/core/wire"
)

var errStripeUnavailable = errors.New("STRIPE_UNAVAILABLE")
var errStripeStream = errors.New("STRIPE_STREAM")
var errStripeSessionLimit = errors.New("SESSION_STREAM_LIMIT")

const maxStripeStreamID uint64 = 8191 // 4,096 odd stream IDs per logical session; ITH9 compatibility bound.

const rekeyRequestToken uint64 = 1 << 63

type stripeSessionKey struct {
	logical    wire.ID
	generation uint64
}

// stripeHub owns server-side logical sessions for one user. Carriers are keyed
// by the authenticated ITH9 logical session/generation, never merely by account.
type stripeHub struct {
	mu           sync.Mutex
	groups       map[stripeSessionKey]*stripeGroup
	cleaning     bool
	cleanupDone  chan struct{}
	idleSince    time.Time
	idleEpoch    uint64
	cleanedEpoch uint64
}

const maxStripeGroupsPerUser = 8

func newStripeHub() *stripeHub {
	return &stripeHub{groups: map[stripeSessionKey]*stripeGroup{}, idleSince: time.Now(), idleEpoch: 1}
}
func (h *stripeHub) group(logical wire.ID, generation uint64) *stripeGroup {
	if h == nil || logical == (wire.ID{}) || generation == 0 {
		return nil
	}
	key := stripeSessionKey{logical: logical, generation: generation}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cleaning {
		return nil
	}
	if g := h.groups[key]; g != nil {
		return g
	}
	if len(h.groups) >= maxStripeGroupsPerUser {
		return nil
	}
	g := newStripeGroup(logical, generation)
	h.idleSince = time.Time{}
	g.hub = h
	g.hubKey = key
	h.groups[key] = g
	return g
}

// attach resolves/creates the authenticated generation and attaches the lane
// while the hub lock is held. This closes the reconnect race where the last
// old carrier could prune an apparently-empty group between lookup and attach.
func (h *stripeHub) attach(l *lane) error {
	if h == nil || l == nil || l.logicalSession == (wire.ID{}) || l.generation == 0 {
		return errStripeUnavailable
	}
	key := stripeSessionKey{logical: l.logicalSession, generation: l.generation}
	h.mu.Lock()
	if h.cleaning {
		h.mu.Unlock()
		return errStripeUnavailable
	}
	g := h.groups[key]
	if g == nil {
		if len(h.groups) >= maxStripeGroupsPerUser {
			h.mu.Unlock()
			return errStripeUnavailable
		}
		g = newStripeGroup(l.logicalSession, l.generation)
		h.idleSince = time.Time{}
		g.hub = h
		g.hubKey = key
		h.groups[key] = g
	}
	h.idleSince = time.Time{}
	err := g.attach(l)
	h.mu.Unlock()
	return err
}

func (h *stripeHub) removeIfEmpty(g *stripeGroup) {
	if h == nil || g == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.groups[g.hubKey] == g && g.empty() {
		delete(h.groups, g.hubKey)
		if len(h.groups) == 0 {
			h.idleSince = time.Now()
			h.idleEpoch++
		}
	}
}

// beginCleanup elects one destructive mailbox janitor only while there is no
// authenticated LogicalSession for this user. New authenticated attaches are
// rejected until endCleanup closes the gate.
func (h *stripeHub) beginCleanup(minIdle time.Duration) (uint64, bool) {
	if h == nil {
		return 0, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cleaning || len(h.groups) != 0 || h.cleanedEpoch == h.idleEpoch || h.idleSince.IsZero() || time.Since(h.idleSince) < minIdle {
		return 0, false
	}
	h.cleaning = true
	h.cleanupDone = make(chan struct{})
	return h.idleEpoch, true
}

func (h *stripeHub) endCleanup(epoch uint64, success bool) {
	if h == nil {
		return
	}
	h.mu.Lock()
	if success && len(h.groups) == 0 && epoch == h.idleEpoch {
		h.cleanedEpoch = epoch
	}
	if h.cleaning {
		h.cleaning = false
		if h.cleanupDone != nil {
			close(h.cleanupDone)
			h.cleanupDone = nil
		}
	}
	h.mu.Unlock()
}

func (h *stripeHub) waitCleanup(ctx context.Context) error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	if !h.cleaning || h.cleanupDone == nil {
		h.mu.Unlock()
		return nil
	}
	ch := h.cleanupDone
	h.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-ch:
		return nil
	}
}

type stripeLocalKey struct {
	account string
	ref     journal.AttemptRef
}

type stripeRemoteKey struct {
	account string
	key     wire.QuotaKey
}

type stripeGroup struct {
	noticeDelivery     noticeDelivery
	preparation        []*preparationWaiter
	preparedByOwner    map[*lane]int
	prepared           int
	capacityNotice     capacityNotification
	lastBulkAt         time.Time
	delivery           deliveryController
	bulkDelivered      atomic.Int64
	downloadDelivered  atomic.Int64
	logical            wire.ID
	generation         uint64
	mu                 sync.Mutex
	lanes              map[string]*lane
	owners             map[uint64]*lane
	streamAccount      map[uint64]string
	feedbackAffinity   map[uint64]string
	feedbackPool       []string
	shortAffinity      map[uint64]string
	interactiveReserve [2]string
	logicalOwners      map[string]*lane
	sessionOwner       *lane
	ownerWG            sync.WaitGroup
	stopped            bool
	// Only one planned key rotation at a time; released by replacement readiness.
	rekeyAccount       string
	configuredAccounts int
	cleanupActive      map[*lane]bool
	cleanupQueue       []*lane
	crossLocal         map[stripeLocalKey][]remoteLogicalAttempt
	crossRemote        map[stripeRemoteKey][]remoteLogicalAttempt
	nextID             uint64
	turn               uint64
	active             atomic.Int64
	fastWaiters        atomic.Int64
	wake               chan struct{}
	fastWake           chan struct{}
	hub                *stripeHub
	hubKey             stripeSessionKey
}

func newStripeGroup(logical wire.ID, generation uint64) *stripeGroup {
	return &stripeGroup{
		delivery: newDeliveryController(),
		logical:  logical, generation: generation, lanes: map[string]*lane{}, owners: map[uint64]*lane{}, streamAccount: map[uint64]string{}, logicalOwners: map[string]*lane{}, cleanupActive: map[*lane]bool{},
		crossLocal: map[stripeLocalKey][]remoteLogicalAttempt{}, crossRemote: map[stripeRemoteKey][]remoteLogicalAttempt{},
		shortAffinity: map[uint64]string{},
		nextID:        1, wake: make(chan struct{}, 1), fastWake: make(chan struct{}, 1),
	}
}

func appendUniqueLogical(dst []remoteLogicalAttempt, owner *lane, ids []uint64) []remoteLogicalAttempt {
	for _, id := range ids {
		if id == 0 || owner == nil {
			continue
		}
		found := false
		for _, old := range dst {
			if old.owner == owner && old.id == id {
				found = true
				break
			}
		}
		if !found {
			dst = append(dst, remoteLogicalAttempt{owner: owner, id: id})
		}
	}
	return dst
}

func (g *stripeGroup) rememberLocal(account string, ref journal.AttemptRef, owner *lane, ids []uint64) {
	if g == nil || account == "" || ref.ID == 0 || owner == nil || len(ids) == 0 {
		return
	}
	g.mu.Lock()
	k := stripeLocalKey{account: account, ref: ref}
	g.crossLocal[k] = appendUniqueLogical(g.crossLocal[k], owner, ids)
	g.mu.Unlock()
}

func (g *stripeGroup) takeLocal(account string, ref journal.AttemptRef) []remoteLogicalAttempt {
	if g == nil || account == "" || ref.ID == 0 {
		return nil
	}
	g.mu.Lock()
	k := stripeLocalKey{account: account, ref: ref}
	out := append([]remoteLogicalAttempt(nil), g.crossLocal[k]...)
	delete(g.crossLocal, k)
	g.mu.Unlock()
	return out
}

func (g *stripeGroup) rememberRemote(account string, key wire.QuotaKey, owner *lane, ids []uint64) {
	if g == nil || account == "" || key.Grant == 0 || owner == nil || len(ids) == 0 {
		return
	}
	g.mu.Lock()
	k := stripeRemoteKey{account: account, key: key}
	g.crossRemote[k] = appendUniqueLogical(g.crossRemote[k], owner, ids)
	g.mu.Unlock()
}

func (g *stripeGroup) takeRemote(account string, key wire.QuotaKey) []remoteLogicalAttempt {
	if g == nil || account == "" || key.Grant == 0 {
		return nil
	}
	g.mu.Lock()
	k := stripeRemoteKey{account: account, key: key}
	out := append([]remoteLogicalAttempt(nil), g.crossRemote[k]...)
	delete(g.crossRemote, k)
	g.mu.Unlock()
	return out
}

func (g *stripeGroup) ownsActiveStream(l *lane) bool {
	if g == nil || l == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, owner := range g.owners {
		if owner == l || (l.account.ID != "" && owner != nil && owner.account.ID == l.account.ID) {
			return true
		}
	}
	return false
}

// hasUnownedReadyCarrier reports whether striped DATA still has at least one
// ready carrier that does not own an active logical stream. Owner lanes normally
// defer mailbox maintenance so OPEN/WINDOW/interactive control stays responsive,
// relying on unowned carriers for physical DATA and cleanup. Once every ready
// carrier owns a stream that assumption no longer holds: deferring cleanup on
// all of them can exhaust the finite remote grants before any lane reaches the
// old per-owner pressure threshold.
func (g *stripeGroup) hasUnownedReadyCarrier() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	owned := make(map[*lane]bool, len(g.owners))
	for _, owner := range g.owners {
		if owner != nil {
			owned[owner] = true
		}
	}
	for _, l := range g.lanes {
		if l != nil && l.streamCarrierReady() && !owned[l] && !g.accountOwnedLocked(l.account.ID) {
			return true
		}
	}
	return false
}

func (g *stripeGroup) signal() {
	if g == nil {
		return
	}
	g.capacityNotice.signal()
	select {
	case g.wake <- struct{}{}:
	default:
	}
	select {
	case g.fastWake <- struct{}{}:
	default:
	}
}

func (g *stripeGroup) beginRekey(l *lane) bool {
	if g == nil || l == nil {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped || g.rekeyAccount != "" || g.lanes[l.account.ID] != l {
		return false
	}
	// If other accounts are configured/attached, preserve at least half of
	// those carriers. Planned rotation must not compound an existing outage.
	ready := 0
	for _, carrier := range g.lanes {
		if carrier != nil && carrier.streamCarrierReady() && carrier.ctx.Err() == nil {
			ready++
		}
	}
	configured := max(g.configuredAccounts, len(g.logicalOwners))
	if configured > 1 && ready-1 < (configured+1)/2 {
		return false
	}
	g.rekeyAccount = l.account.ID
	return true
}

func (g *stripeGroup) completeRekey(l *lane) {
	if g == nil || l == nil {
		return
	}
	g.mu.Lock()
	if g.rekeyAccount == l.account.ID && g.lanes[l.account.ID] == l && l.streamCarrierReady() {
		g.rekeyAccount = ""
	}
	g.mu.Unlock()
}

// wakeReaders propagates the logical-session activity transition to every
// carrier. A reader may already be sleeping in the zero-stream 250ms..2s
// backoff; changing g.active alone does not interrupt that timer.
func (g *stripeGroup) wakeReaders() {
	if g == nil {
		return
	}
	g.mu.Lock()
	lanes := make([]*lane, 0, len(g.lanes))
	for _, l := range g.lanes {
		if l != nil {
			lanes = append(lanes, l)
		}
	}
	g.mu.Unlock()
	for _, l := range lanes {
		l.wakeReader()
	}
}

func (g *stripeGroup) attach(l *lane) error {
	if g == nil || l == nil || l.logicalSession != g.logical || l.generation != g.generation {
		return errStripeUnavailable
	}
	l.stripe = g
	if notifier, ok := l.mail.(interface{ SetDataWake(func()) }); ok {
		notifier.SetDataWake(g.signal)
	}
	if l.physicalSlots == nil {
		// Capacity may shrink and later recover as DATA writers die/redial. Keep
		// the channel at the hard maximum and enforce the current live limit in
		// tryAcquirePhysicalSlot instead of freezing attach-time parallelism.
		l.physicalSlots = make(chan struct{}, 4)
		l.fastPhysicalSlots = make(chan struct{}, 1)
	}
	g.mu.Lock()
	// Each endpoint's first authenticated carrier fixes its directional
	// logical flight ceiling before any bulk admission. Reconnects retain it.
	g.delivery.setRoleWindow(l.side)
	old := g.lanes[l.account.ID]
	if old != nil && old != l && l.carrierEpoch < old.carrierEpoch {
		g.mu.Unlock()
		return errStripeUnavailable
	}
	if owner := g.logicalOwners[l.account.ID]; owner != nil && owner != l {
		l.rejoinPending.Store(true)
	}
	g.lanes[l.account.ID] = l
	g.mu.Unlock()
	g.signal()
	if g.activeStreams() > 0 {
		l.wakeReader()
	}
	return nil
}

func (g *stripeGroup) detach(l *lane) {
	if g == nil || l == nil {
		return
	}
	g.mu.Lock()
	if g.lanes[l.account.ID] == l {
		delete(g.lanes, l.account.ID)
	}
	g.removeCleanupLocked(l)
	g.wakeCleanupLocked(g.cleanupChoiceLocked())
	// Real logical actors survive carrier detach. Legacy direct test owners
	// retain tombstone behavior. IDs remain monotonic and bounded per generation.
	for id, owner := range g.owners {
		if owner == l && !l.ownerStarted {
			g.owners[id] = nil
			delete(g.shortAffinity, id)
			delete(g.streamAccount, id)
			delete(g.feedbackAffinity, id)
			owner.admitted.Add(-1)
			g.active.Add(-1)
		}
	}
	g.mu.Unlock()
	// Cross-carrier logical ownership is retained by stripeGroup itself. The
	// physical journal/wallet on this account remains fail-closed and is recovered
	// by the replacement carrier; losing one physical lane must never cancel
	// unrelated logical owners and cascade through the whole striped session.
	g.signal()
	if g.hub != nil {
		g.hub.removeIfEmpty(g)
	}
}

func (g *stripeGroup) allocate(owner *lane) (uint64, error) {
	if g == nil || owner == nil {
		return 0, errStripeUnavailable
	}
	g.mu.Lock()
	if g.stopped || g.nextID == 0 || g.nextID > maxStripeStreamID {
		g.mu.Unlock()
		return 0, errStripeSessionLimit
	}
	if max(owner.admitted.Load(), owner.active.Load()) >= runtimeOwnerStreams {
		g.mu.Unlock()
		return 0, socks.OpenError{Code: wire.ReasonCapacity}
	}
	id := g.nextID
	g.nextID += 2
	if _, exists := g.owners[id]; exists {
		g.mu.Unlock()
		return 0, errStripeStream
	}
	wakeReaders := g.active.Load() == 0
	g.owners[id] = owner
	if g.streamAccount == nil {
		g.streamAccount = map[uint64]string{}
	}
	g.streamAccount[id] = owner.account.ID
	owner.admitted.Add(1)
	g.active.Add(1)
	g.mu.Unlock()
	g.signal()
	if wakeReaders {
		g.wakeReaders()
	}
	return id, nil
}

func (g *stripeGroup) register(id uint64, owner *lane) error {
	if g == nil || owner == nil || id == 0 || id%2 != 1 || id > maxStripeStreamID {
		return errStripeStream
	}
	g.mu.Lock()
	if old, exists := g.owners[id]; exists {
		g.mu.Unlock()
		if old == owner {
			return nil
		}
		return errStripeStream
	}
	wakeReaders := g.active.Load() == 0
	g.owners[id] = owner
	if g.streamAccount == nil {
		g.streamAccount = map[uint64]string{}
	}
	g.streamAccount[id] = owner.account.ID
	owner.admitted.Add(1)
	g.active.Add(1)
	g.mu.Unlock()
	g.signal()
	if wakeReaders {
		g.wakeReaders()
	}
	return nil
}

func (g *stripeGroup) retire(id uint64, owner *lane) {
	if g == nil || id == 0 {
		return
	}
	g.mu.Lock()
	if old, exists := g.owners[id]; exists && old != nil && (owner == nil || old == owner) {
		g.owners[id] = nil
		delete(g.shortAffinity, id)
		delete(g.streamAccount, id)
		delete(g.feedbackAffinity, id)
		old.admitted.Add(-1)
		g.active.Add(-1)
	}
	g.mu.Unlock()
	g.signal()
}

func (g *stripeGroup) owner(id uint64) *lane {
	o, _ := g.route(id)
	return o
}

// route distinguishes an authenticated late frame for a retired stream from
// a frame naming an ID that was never allocated in this LogicalSession.
func (g *stripeGroup) route(id uint64) (*lane, bool) {
	if g == nil || id == 0 {
		return nil, false
	}
	g.mu.Lock()
	o, known := g.owners[id]
	g.mu.Unlock()
	return o, known
}
func (g *stripeGroup) activeStreams() int64 {
	if g == nil {
		return 0
	}
	return g.active.Load()
}

func (g *stripeGroup) empty() bool {
	if g == nil {
		return true
	}
	g.mu.Lock()
	empty := len(g.lanes) == 0 && len(g.logicalOwners) == 0 && g.active.Load() == 0
	g.mu.Unlock()
	return empty
}

func (g *stripeGroup) capacity() int {
	if g == nil {
		return 1
	}
	// Pump worker count is fixed at start while carriers attach asynchronously.
	// Bounded workers can keep configured accounts busy without increasing byte,
	// grant or queue bound; per-carrier physicalSlots still enforce its actual
	// IMAP writer concurrency.
	return datapath.MaxBulkWriters
}

type stripeLease struct {
	lane  *lane
	group *stripeGroup
	fast  bool
}

func (x stripeLease) release() {
	if x.lane != nil && x.lane.physicalSlots != nil {
		select {
		case <-classSlotChannel(x.lane, x.fast):
		default:
		}
	}
	if x.group != nil {
		x.group.signal()
	}
}

func (g *stripeGroup) acquire(ctx context.Context) (stripeLease, error) {
	return g.acquireForClass(ctx, nil, false)
}

// acquireFor prefers a physical carrier different from the logical owner. DATA
// socket failures are much more common than control/session failures on public
// IMAP, so keeping the owner off its own DATA writers whenever another carrier
// is healthy prevents one poisoned APPEND connection from taking stream/control
// ownership down with it. The owner remains a last-resort carrier.
func (g *stripeGroup) acquireFor(ctx context.Context, avoid *lane) (stripeLease, error) {
	return g.acquireForClass(ctx, avoid, false)
}

// selectFor chooses the physical carrier without consuming one of its scarce
// APPEND slots. Grant acquisition may block for seconds under mailbox debt, so
// holding a slot while waiting for a grant would let bulk traffic starve an
// otherwise grant-ready interactive batch.
func (g *stripeGroup) selectFor(ctx context.Context, avoid *lane) (*lane, error) {
	return g.selectForClass(ctx, avoid, false)
}

func (g *stripeGroup) selectForClass(ctx context.Context, avoid *lane, fast bool) (*lane, error) {
	if g == nil {
		return nil, errStripeUnavailable
	}
	for {
		capacityWake := g.capacityNotice.watch()
		g.mu.Lock()
		reservedReader := g.interactiveReadersLocked()
		keys := make([]string, 0, len(g.lanes))
		for id := range g.lanes {
			keys = append(keys, id)
		}
		sort.Strings(keys)
		if fast {
			g.prioritizeFastTargetsLocked(keys, avoid)
		} else {
			g.prioritizeBulkTargetsLocked(keys)
		}
		if len(keys) > 0 {
			start := 0
			cleanupPasses := 1
			if fast {
				cleanupPasses = 2
			}
			for cleanupPass := 0; cleanupPass < cleanupPasses; cleanupPass++ {
				for pass := 0; pass < 2; pass++ {
					for off := 0; off < len(keys); off++ {
						idx := (start + off) % len(keys)
						l := g.lanes[keys[idx]]
						if !fast && (l == reservedReader[0] || l == reservedReader[1]) {
							continue
						}
						if l == nil || !l.streamCarrierReady() || l.ctx.Err() != nil || l.physicalSlots == nil {
							continue
						}
						if fast && l.controlDebt.Load() >= 80 {
							continue
						}
						if !fast && l.bulkControlBlocked.Load() {
							continue
						}
						if fast && cleanupPass == 0 && l.cleanupBlocksFast() {
							continue
						}
						if fast && cleanupPass == 1 && !l.cleanupBlocksFast() {
							continue
						}
						if pass == 0 && avoid != nil && l == avoid {
							continue
						}
						if pass == 1 && (avoid == nil || l != avoid) {
							continue
						}
						if availability, ok := l.mail.(interface{ DataAvailable() bool }); ok && !availability.DataAvailable() {
							continue
						}
						g.turn++
						g.mu.Unlock()
						return l, nil
					}
					if avoid == nil {
						break
					}
				}
			}
		}
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-capacityWake:
		}
	}
}

func (g *stripeGroup) takeRemoteGrantForClass(ctx context.Context, avoid *lane, fast bool) (*lane, wire.QuotaKey, error) {
	return g.takeRemoteGrantPreferred(ctx, avoid, nil, fast)
}

func (g *stripeGroup) takeRemoteGrantPreferred(ctx context.Context, avoid, preferred *lane, fast bool) (*lane, wire.QuotaKey, error) {
	var grace <-chan time.Time
	if fast {
		g.mu.Lock()
		budget := g.fastSlotGraceLocked()
		g.mu.Unlock()
		timer := time.NewTimer(budget)
		defer timer.Stop()
		grace = timer.C
	}
	fallback := false
	if g == nil {
		return nil, wire.QuotaKey{}, errStripeUnavailable
	}
	for {
		capacityWake := g.capacityNotice.watch()
		g.mu.Lock()
		reservedReader := g.interactiveReadersLocked()
		fastCeiling := int64(1 << 62)
		if fast {
			fastCeiling = g.fastLatencyCeilingLocked()
		}
		keys := make([]string, 0, len(g.lanes))
		for id := range g.lanes {
			keys = append(keys, id)
		}
		sort.Strings(keys)
		if fast {
			g.prioritizeFastTargetsLocked(keys, avoid)
			g.preferShortTargetLocked(keys, preferred)
		} else {
			g.prioritizeBulkTargetsLocked(keys)
		}
		if len(keys) > 0 {
			start := 0
			if fast {
				start = 0
			}
			for pass := 0; pass < 2; pass++ {
				for off := 0; off < len(keys); off++ {
					idx := (start + off) % len(keys)
					l := g.lanes[keys[idx]]
					if !fast && (l == reservedReader[0] || l == reservedReader[1]) {
						continue
					}
					if l == nil || !l.openHealthy(time.Now()) || l.physicalSlots == nil {
						continue
					}
					if fast && l.controlDebt.Load() >= 80 {
						continue
					}
					if fast && !fallback && g.carrierLatencyLocked(l) > fastCeiling {
						continue
					}
					if !fast && l.bulkControlBlocked.Load() {
						continue
					}
					if fast && l.cleanupBlocksFast() {
						continue
					}
					if !fast && pass == 0 && avoid != nil && l == avoid {
						continue
					}
					if !fast && pass == 1 && (avoid == nil || l != avoid) {
						continue
					}
					if fast && classSlotsFull(l, true) {
						continue
					}
					if availability, ok := l.mail.(interface{ DataAvailable() bool }); ok && !availability.DataAvailable() {
						continue
					}
					if key, ok := l.tryTakeRemoteGrant(fast); ok {
						if fast {
							g.turn++
						} else {
							g.turn++
						}
						g.mu.Unlock()
						return l, key, nil
					}
				}
				if fast || avoid == nil {
					break
				}
			}
		}
		g.mu.Unlock()
		wake := capacityWake
		if fast {
			wake = capacityWake
		}
		select {
		case <-ctx.Done():
			return nil, wire.QuotaKey{}, ctx.Err()
		case <-grace:
			fallback = true
		case <-wake:
		}
	}
}

func physicalSlotLimit(l *lane) int {
	if l == nil || l.physicalSlots == nil {
		return 0
	}
	if l.mail == nil {
		return cap(l.physicalSlots)
	}
	n := dataParallelism(l.mail)
	if n < 1 {
		n = 1
	}
	if n > 4 {
		n = 4
	}
	if n > cap(l.physicalSlots) {
		n = cap(l.physicalSlots)
	}
	return n
}

func tryAcquirePhysicalSlot(l *lane) bool {
	limit := physicalSlotLimit(l)
	if limit == 0 || len(l.physicalSlots) >= limit {
		return false
	}
	select {
	case l.physicalSlots <- struct{}{}:
		return true
	default:
		return false
	}
}

// Reserve immediately after fast grant selection. A competing fast writer can
// win this slot, so failure must return the unused grant and choose again.
// Global fast priority blocks new bulk reservations only until this succeeds.
func (g *stripeGroup) tryAcquireFastLane(target *lane) (stripeLease, bool) {
	if g == nil || target == nil {
		return stripeLease{}, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	attached := false
	for _, l := range g.lanes {
		if l == target {
			attached = true
			break
		}
	}
	if !attached || !target.streamCarrierReady() || target.ctx.Err() != nil {
		return stripeLease{}, false
	}
	if available, ok := target.mail.(interface{ DataAvailable() bool }); ok && !available.DataAvailable() {
		return stripeLease{}, false
	}
	if !tryAcquireClassSlot(target, true) {
		return stripeLease{}, false
	}
	return stripeLease{lane: target, group: g, fast: true}, true
}

// acquireLaneForClass reserves a slot on one already selected carrier. Fast
// waiters retain global priority over newly arriving bulk reservations, while
// the live DataParallelism check shrinks capacity immediately after a writer
// dies instead of trusting the channel size captured at attach time.
func (g *stripeGroup) acquireLaneForClass(ctx context.Context, target *lane, fast bool) (stripeLease, error) {
	if g == nil || target == nil {
		return stripeLease{}, errStripeUnavailable
	}
	if fast {
		g.fastWaiters.Add(1)
		g.signal()
		defer func() {
			g.fastWaiters.Add(-1)
			g.signal()
		}()
	}
	for {
		capacityWake := g.capacityNotice.watch()
		g.mu.Lock()
		if !fast && g.fastWaiters.Load() > 0 {
			g.mu.Unlock()
		} else {
			attached := false
			for _, l := range g.lanes {
				if l == target {
					attached = true
					break
				}
			}
			if !attached || !target.streamCarrierReady() || target.ctx.Err() != nil {
				g.mu.Unlock()
				return stripeLease{}, errStripeUnavailable
			}
			if availability, ok := target.mail.(interface{ DataAvailable() bool }); ok && !availability.DataAvailable() {
				g.mu.Unlock()
				return stripeLease{}, errStripeUnavailable
			}
			if tryAcquireClassSlot(target, fast) {
				g.mu.Unlock()
				g.signal()
				return stripeLease{lane: target, group: g, fast: fast}, nil
			}
			g.mu.Unlock()
		}
		wake := capacityWake
		if fast {
			wake = capacityWake
		}
		select {
		case <-ctx.Done():
			return stripeLease{}, ctx.Err()
		case <-wake:
		}
	}
}

// acquireForClass gives latency-sensitive DATA the next physical APPEND slot
// once it is waiting. Bulk writers already in flight are never preempted, but
// newly scheduled bulk batches cannot repeatedly win the same released slot and
// stretch one short exchange across several provider RTTs.
func (g *stripeGroup) acquireForClass(ctx context.Context, avoid *lane, fast bool) (stripeLease, error) {
	if g == nil {
		return stripeLease{}, errStripeUnavailable
	}
	if fast {
		g.fastWaiters.Add(1)
		g.signal()
		defer func() {
			g.fastWaiters.Add(-1)
			g.signal()
		}()
	}
	for {
		capacityWake := g.capacityNotice.watch()
		g.mu.Lock()
		reservedReader := g.interactiveReadersLocked()
		if !fast && g.fastWaiters.Load() > 0 {
			g.mu.Unlock()
			select {
			case <-ctx.Done():
				return stripeLease{}, ctx.Err()
			case <-capacityWake:
			}
			continue
		}
		keys := make([]string, 0, len(g.lanes))
		for id := range g.lanes {
			keys = append(keys, id)
		}
		sort.Strings(keys)
		if fast {
			g.prioritizeFastTargetsLocked(keys, avoid)
		} else {
			g.prioritizeBulkTargetsLocked(keys)
		}
		if len(keys) > 0 {
			start := 0
			for pass := 0; pass < 2; pass++ {
				for off := 0; off < len(keys); off++ {
					idx := (start + off) % len(keys)
					l := g.lanes[keys[idx]]
					if !fast && (l == reservedReader[0] || l == reservedReader[1]) {
						continue
					}
					if l == nil || !l.streamCarrierReady() || l.ctx.Err() != nil || l.physicalSlots == nil {
						continue
					}
					if pass == 0 && avoid != nil && l == avoid {
						continue
					}
					if pass == 1 && (avoid == nil || l != avoid) {
						continue
					}
					if availability, ok := l.mail.(interface{ DataAvailable() bool }); ok && !availability.DataAvailable() {
						continue
					}
					if tryAcquireClassSlot(l, fast) {
						g.turn++
						g.mu.Unlock()
						g.signal()
						return stripeLease{lane: l, group: g, fast: fast}, nil
					}
				}
				if avoid == nil {
					break
				}
			}
		}
		g.mu.Unlock()
		wake := capacityWake
		if fast {
			wake = capacityWake
		}
		select {
		case <-ctx.Done():
			return stripeLease{}, ctx.Err()
		case <-wake:
		}
	}
}

func (g *stripeGroup) maxRemoteSize(f wire.Frame) (uint64, error) {
	if g == nil {
		return 0, errStripeUnavailable
	}
	g.mu.Lock()
	lanes := make([]*lane, 0, len(g.lanes))
	for _, l := range g.lanes {
		if l != nil && l.streamCarrierReady() {
			lanes = append(lanes, l)
		}
	}
	g.mu.Unlock()
	var maximum uint64
	var first error
	for _, l := range lanes {
		if l.cipher == nil {
			continue
		}
		k := wire.QuotaKey{Store: wire.ID{1}, Epoch: 1, Grant: 1, Session: l.cipher.Context().Session, User: l.user.ID, Account: l.account.ID}
		n, err := datapath.Size(f, &k)
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		if n > maximum {
			maximum = n
		}
	}
	if maximum == 0 {
		if first != nil {
			return 0, first
		}
		return 0, errStripeUnavailable
	}
	return maximum, nil
}

// claimOwner never transfers physical state. A replacement account carrier uses
// the existing logical actor and its original bounded flow/socket budget.
func (g *stripeGroup) claimOwner(l *lane) (*lane, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped {
		return nil, errStripeUnavailable
	}
	if g.logicalOwners == nil {
		g.logicalOwners = make(map[string]*lane)
	}
	if g.sessionOwner != nil {
		if g.sessionOwner.logicalContext().Err() != nil {
			return nil, errStripeUnavailable
		}
		return g.sessionOwner, nil
	}
	if owner := g.logicalOwners[l.account.ID]; owner != nil {
		if owner.logicalContext().Err() != nil {
			return nil, errStripeUnavailable
		}
		return owner, nil
	}
	g.ownerWG.Add(1)
	g.logicalOwners[l.account.ID] = l
	g.sessionOwner = l
	return l, nil
}
func (g *stripeGroup) accountOwner(account string) *lane {
	g.mu.Lock()
	defer g.mu.Unlock()
	owner := g.sessionOwner
	if owner == nil {
		owner = g.logicalOwners[account]
	}
	if owner == nil || owner.logicalContext().Err() != nil {
		return nil
	}
	select {
	case <-owner.actorReady:
		return owner
	default:
		return nil
	}
}
func (g *stripeGroup) releaseOwner(l *lane) {
	g.mu.Lock()
	if g.logicalOwners[l.account.ID] == l {
		delete(g.logicalOwners, l.account.ID)
		if g.sessionOwner == l {
			g.sessionOwner = nil
		}
		g.ownerWG.Done()
	}
	for id, owner := range g.owners {
		if owner == l {
			g.owners[id] = nil
			owner.admitted.Add(-1)
			g.active.Add(-1)
		}
	}
	g.mu.Unlock()
	g.signal()
	if g.hub != nil {
		g.hub.removeIfEmpty(g)
	}
}
func (g *stripeGroup) stopOwners() {
	g.mu.Lock()
	g.stopped = true
	for _, owner := range g.logicalOwners {
		owner.ownerCancel()
	}
	g.mu.Unlock()
}
func (g *stripeGroup) progressAge(now time.Time) time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	latest := int64(0)
	for _, l := range g.lanes {
		if p := l.lastProgress.Load(); p > latest {
			latest = p
		}
	}
	// During an outage keep the last authenticated progress timestamp from owners.
	for _, l := range g.logicalOwners {
		if p := l.lastProgress.Load(); p > latest {
			latest = p
		}
	}
	if latest == 0 {
		return transportProgressLimit + time.Second
	}
	return now.Sub(time.Unix(0, latest))
}

// Stream controls use a negotiated capability and an authenticated carrier in
// the same user/logical session/generation. Quota and cleanup controls stay
// strictly bound to their original physical account.
func (g *stripeGroup) sendStreamControl(ctx context.Context, preferred *lane, f wire.Frame) error {
	for {
		capacityWake := g.capacityNotice.watch()
		g.mu.Lock()
		candidates := make([]*lane, 0, len(g.lanes))
		preferredAccount := preferred.account.ID
		if f.Type == wire.Open && g.streamAccount[f.Stream] != "" {
			// The logical actor can belong to the first physical account long
			// after that carrier has been replaced. Route OPEN on the carrier
			// Client.Open selected and monitors, rather than sending every new
			// stream back to that actor's original account after reconnect.
			preferredAccount = g.streamAccount[f.Stream]
		}
		if p := g.lanes[preferredAccount]; p != nil && p.streamCarrierReady() && p.ctx.Err() == nil {
			candidates = append(candidates, p)
		}
		for _, l := range g.lanes {
			if l.account.ID != preferredAccount && l.streamCarrierReady() && l.ctx.Err() == nil {
				candidates = append(candidates, l)
			}
		}
		// Bulk cumulative receive feedback shares stable non-reserved readers.
		// Otherwise ordinary per-record credit/ACK traffic consumes the quiet
		// mailboxes reserved for parallel OPEN and short request/reply progress.
		bulkFeedback := bulkReceiveFeedback(f)
		if bulkFeedback {
			reserve := g.interactiveReadersLocked()
			ordinary := make([]*lane, 0, len(candidates))
			for _, c := range candidates {
				if c != reserve[0] && c != reserve[1] {
					ordinary = append(ordinary, c)
				}
			}
			if len(ordinary) > 0 {
				candidates = ordinary
			}
		}
		// Stream controls can use the negotiated logical route. Preserve OPEN
		// affinity, but do not pin WINDOW/receipt/repair to a throttled carrier
		// while authenticated siblings have an empty control path.
		if f.Type != wire.Open {
			score := func(l *lane) int {
				// An empty APPEND queue does not imply a readable peer mailbox:
				// outstanding BODYs can still block WINDOW/RECEIPT behind bulk.
				if bulkFeedback {
					return controlCarrierScore(l) + int(g.carrierLatencyLocked(l))
				}
				return interactiveCarrierScore(l, [2]*lane{}) + int(g.carrierLatencyLocked(l)) + int(g.delivery.carrierBytes[l.account.ID]/(32<<10))
			}
			sort.SliceStable(candidates, func(i, j int) bool { return score(candidates[i]) < score(candidates[j]) })
			if bulkFeedback {
				candidates = g.orderBulkFeedbackLocked(candidates, f)
			}
			if len(candidates) > 0 && !bulkFeedback {
				affinity := g.shortAffinity[f.Stream]
				if bulkFeedback {
					affinity = g.feedbackAffinity[f.Stream]
				}
				if affinity == "" {
					affinity = preferred.account.ID
				}
				for i, c := range candidates {
					initialCredit := f.Type == wire.Window && f.Offset == runtimeStreamWindow
					provenInitial := initialCredit && c.controlStallPenalty(time.Now()) == 0 && c.account.ID == affinity && c.controlDebt.Load() < 80 && len(c.urgentQueue) < 48 && (c.independentDataMaintenance() || len(c.physicalSlots) == 0)
					if provenInitial {
						for _, r := range g.delivery.ranges {
							if !r.fast && r.carrier == c.account.ID {
								provenInitial = false
								break
							}
						}
					}
					// OPEN_OK proved a working peer reader. Its first receive credit
					// should share that path; independent short DATA occupancy is not
					// a reason to add a fresh slower control leg before any reply.
					pendingFeedback := bulkFeedback && c.hasPendingFlowControl(f.Type, f.Stream)
					if c.account.ID == affinity && (pendingFeedback || provenInitial || score(c) <= score(candidates[0])+75) {
						copy(candidates[1:i+1], candidates[:i])
						candidates[0] = c
						break
					}
				}
			}
		}
		g.mu.Unlock()
		for _, l := range candidates {
			if err := l.sendCarrierControl(ctx, f); err == nil {
				if bulkFeedback {
					g.mu.Lock()
					if g.owners[f.Stream] != nil {
						if g.feedbackAffinity == nil {
							g.feedbackAffinity = map[uint64]string{}
						}
						g.feedbackAffinity[f.Stream] = l.account.ID
					}
					g.mu.Unlock()
				}
				return nil
			} else if l.ctx.Err() == nil && errorCode(err) != "CONTROL_QUEUE_LIMIT" {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-capacityWake:
		case <-time.After(25 * time.Millisecond):
		}
	}
}
func (g *stripeGroup) dispatchStream(carrier *lane, f wire.Frame) error {
	owner, known := g.route(f.Stream)
	if !known && f.Type == wire.Open {
		if !carrier.side {
			return errors.New("OPEN_DIRECTION")
		}
		owner = g.accountOwner(carrier.account.ID)
		if owner == nil {
			return errStripeUnavailable
		}
		if err := g.register(f.Stream, owner); err != nil {
			return err
		}
		known = true
	}
	if !known {
		return errStripeStream
	}
	if owner == nil || owner.logicalContext().Err() != nil {
		clear(f.Payload)
		return nil
	}
	if f.Type == wire.Open || f.Type == wire.OpenOK || f.Type == wire.Data && len(f.Payload) <= datapath.InteractiveDataThreshold {
		g.mu.Lock()
		if g.owners[f.Stream] != nil {
			if g.shortAffinity == nil {
				g.shortAffinity = map[uint64]string{}
			}
			g.shortAffinity[f.Stream] = carrier.account.ID
		}
		g.mu.Unlock()
	}
	if owner.ownerFrames == nil {
		return owner.deliverOwned(f)
	}
	// The actor has 32 frame slots; Pump inputs have 224, retaining the existing
	// combined 256-frame incoming bound. Copy only when the authenticated
	// decode owns a buffer that the mailbox reader may subsequently clear.
	select {
	case owner.ownerFrames <- f:
		return nil
	case <-owner.logicalContext().Done():
		clear(f.Payload)
		return nil
	case <-carrier.ctx.Done():
		return carrier.ctx.Err()
	}
}

func (g *stripeGroup) accountOwnedLocked(account string) bool {
	if account == "" {
		return false
	}
	for _, owner := range g.owners {
		if owner != nil && owner.account.ID == account {
			return true
		}
	}
	return false
}

func (g *stripeGroup) carrierStreamCount(account string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.sessionOwner == nil {
		if owner := g.logicalOwners[account]; owner != nil {
			return int(owner.active.Load())
		}
		return 0
	}
	count := 0
	for id, owner := range g.owners {
		if owner != nil && g.streamAccount[id] == account {
			count++
		}
	}
	return count
}
