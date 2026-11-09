package mailstream

import (
	"context"
	"errors"
	"fmt"
	"time"

	imap "github.com/emersion/go-imap/v2"
)

// Amortize audited metadata work over a larger bounded generation. This does
// not batch interactive writes: each small packet is still published promptly.
const segmentAppends = 128

var errCollectionPartial = errors.New("MAILSTREAM_COLLECTION_PARTIAL")

// Normally two slots alternate. Unavailable housekeeping may retain additional
// slots, within both this hard bound and the unchanged physical byte budget.
const dataSlots uint32 = 64

type Retirement struct {
	Session    SessionID
	Account    string
	Generation uint32
	Kind       byte
	Slot       uint32
}
type StorageLink interface {
	Retirements() <-chan Retirement
	Acknowledge(SessionID, uint64) error
	ConfirmRetired(Retirement) error
	RequestCollection(Retirement) error
	ConfirmCollection(Retirement) error
	ConfirmRoute(Retirement) error
}
type segmentKey struct {
	session    SessionID
	generation uint32
}
type segmentRecord struct {
	folder                                                 string
	slot                                                   uint32
	bytes, appendPending, appends                          int
	awaiting                                               map[uint64]bool
	uids                                                   imap.UIDSet
	validity                                               uint32
	proofUnknown                                           bool
	retired, queued, refused, remote, requested, announced bool
	collected                                              bool
	readyAt                                                time.Time
}
type cleanupJob struct {
	account *mailAccountState
	key     segmentKey
	record  *segmentRecord
}

func (l *IMAPLink) segmentFolder(id SessionID, out bool, slot uint32) string {
	return fmt.Sprintf("%s-%02x", l.dataFolder(id, out), slot)
}
func (l *IMAPLink) Retirements() <-chan Retirement { return l.retirements }
func (l *IMAPLink) Acknowledge(id SessionID, sequence uint64) error {
	return l.releaseDelivery(id, sequence, true)
}
func (l *IMAPLink) Abandon(id SessionID, sequence uint64) {
	if l.releaseDelivery(id, sequence, false) != nil {
		l.cancel()
	}
}
func (l *IMAPLink) releaseDelivery(id SessionID, sequence uint64, measured bool) error {
	for _, a := range l.accounts {
		a.mu.Lock()
		sample := &a.delivery[sequence%replaySlots]
		if sample.id == id && sample.sequence == sequence && !sample.started.IsZero() {
			if measured && !sample.retried {
				a.rtt, a.rttAt = time.Since(sample.started), time.Duration(time.Now().UnixNano())
			}
			*sample = deliverySample{}
		}
		for key, r := range a.segments {
			if key.session == id {
				delete(r.awaiting, sequence)
			}
		}
		a.mu.Unlock()
	}
	return nil
}
func (l *IMAPLink) account(name string) *mailAccountState {
	for _, a := range l.accounts {
		if a.config.ID == name {
			return a
		}
	}
	return nil
}
func (l *IMAPLink) ConfirmRetired(r Retirement) error {
	a := l.account(r.Account)
	if a == nil {
		return ErrProtocol
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if rec := a.segments[segmentKey{r.Session, r.Generation}]; rec != nil {
		rec.retired = true
	}
	return nil
}
func (l *IMAPLink) RequestCollection(r Retirement) error {
	if l.client {
		return ErrProtocol
	}
	a := l.account(r.Account)
	if a == nil {
		return ErrProtocol
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if rec := a.remoteSegments[segmentKey{r.Session, r.Generation}]; rec != nil {
		rec.requested = true
	}
	return nil
}
func (l *IMAPLink) ConfirmCollection(r Retirement) error {
	if !l.client {
		return ErrProtocol
	}
	a := l.account(r.Account)
	if a == nil {
		return ErrProtocol
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	key := segmentKey{r.Session, r.Generation}
	rec := a.segments[key]
	if rec == nil {
		return nil
	}
	if !rec.queued || !rec.retired || rec.appendPending != 0 || len(rec.awaiting) != 0 {
		return ErrProtocol
	}
	if err := a.refundLocked(rec.bytes); err != nil {
		return err
	}
	delete(a.segments, key)
	a.retired.Add(1)
	a.bulkBlocked = false
	return nil
}
func (l *IMAPLink) ConfirmRoute(r Retirement) error {
	if r.Slot >= dataSlots {
		return ErrProtocol
	}
	a := l.account(r.Account)
	if a == nil {
		return ErrProtocol
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.routeSession != r.Session {
		a.routeSession = r.Session
		a.routes = map[uint32]uint32{}
		a.routeConsumed = 0
	}
	if r.Generation <= a.routeConsumed {
		return nil
	}
	if old, ok := a.routes[r.Generation]; ok {
		if old != r.Slot {
			return ErrProtocol
		}
		return nil
	}
	if len(a.routes) >= int(dataSlots) {
		return ErrCapacity
	}
	a.routes[r.Generation] = r.Slot
	close(a.routesChanged)
	a.routesChanged = make(chan struct{})
	return nil
}

// Called under Account.mu by the sole APPEND actor. A slot remains occupied
// until verified empty collection, terminal APPENDs and authenticated receipts.
func (a *mailAccountState) prepareSegmentLocked(id SessionID, generation uint32) (*segmentRecord, error) {
	key := segmentKey{id, generation}
	rec := a.segments[key]
	if rec == nil {
		var used [dataSlots]bool
		for k, r := range a.segments {
			if k.session == id {
				used[r.slot] = true
			}
		}
		var slot uint32
		for slot < dataSlots && used[slot] {
			slot++
		}
		if slot == dataSlots {
			a.bulkBlocked = true
			return nil, ErrCapacity
		}
		rec = &segmentRecord{folder: a.link.segmentFolder(id, true, slot), slot: slot, awaiting: map[uint64]bool{}}
		a.segments[key] = rec
	}
	if generation > 0 && !rec.announced {
		select {
		case a.link.retirements <- Retirement{Session: id, Account: a.config.ID, Generation: generation, Kind: frameSegment, Slot: rec.slot}:
			rec.announced = true
		default:
			return nil, ErrCapacity
		}
	}
	a.bulkBlocked = false
	return rec, nil
}
func (a *mailAccountState) waitRoute(ctx context.Context, p *mailPipe, id SessionID, generation uint32) (uint32, error) {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		a.mu.Lock()
		slot, ok := a.routes[generation]
		changed := a.routesChanged
		if a.routeSession == id && ok {
			delete(a.routes, generation)
			a.routeConsumed = generation
			a.mu.Unlock()
			return slot, nil
		}
		a.mu.Unlock()
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-p.done:
			return 0, ErrClosed
		case <-changed:
		case <-tick.C:
			stop := p.limit(10 * time.Second)
			err := p.client.Noop().Wait()
			stop()
			if err != nil {
				return 0, err
			}
		}
	}
}
func (a *mailAccountState) configureSegments(p *mailPipe, id SessionID) {
	generation, slot := uint32(0), uint32(0)
	p.rotate = func(ctx context.Context) error {
		if generation == ^uint32(0) {
			return ErrCapacity
		}
		box := p.client.Mailbox()
		uids, ok := p.consumed.Nums()
		if box == nil || box.Name != a.link.segmentFolder(id, false, slot) || box.NumMessages != segmentAppends || p.after != segmentAppends || !ok || len(uids) != segmentAppends {
			return ErrProtocol
		}
		proof := &segmentRecord{folder: box.Name, slot: slot, uids: append(imap.UIDSet(nil), p.consumed...), validity: p.validity, bytes: int(p.consumedMIMEBytes.Load()), remote: true, retired: true}
		// SELECT implicitly leaves the drained mailbox without expunging it. No
		// storage mutation or reclamation command runs on the delivery reader.
		parking := a.link.dataFolder(id, false) + "-p"
		if _, err := p.selectLog(parking, false); err != nil {
			return err
		}
		if !a.link.client {
			a.mu.Lock()
			a.remoteSegments[segmentKey{id, generation}] = proof
			a.mu.Unlock()
		}
		select {
		case a.link.retirements <- Retirement{Session: id, Account: a.config.ID, Generation: generation, Kind: frameRetired}:
		case <-ctx.Done():
			return ctx.Err()
		}
		generation++
		p.generation = generation
		p.generationDebug.Store(generation)
		p.phase.Store(10)
		next, err := a.waitRoute(ctx, p, id, generation)
		if err != nil {
			return err
		}
		slot = next
		if _, err = p.selectLog(a.link.segmentFolder(id, false, slot), false); err != nil {
			return err
		}
		return nil
	}
}

// Prefer the direction near its unchanged physical cap, using consumed MIME
// bytes for the current incoming generation as well as pending retired ones.
// Both pressured directions (or neither) retain the established alternation.
func (a *mailAccountState) preferRemoteCollectionLocked() bool {
	var remoteBytes int64
	if a.data != nil {
		remoteBytes = a.data.consumedMIMEBytes.Load()
	}
	for _, rec := range a.remoteSegments {
		if rec.retired && rec.requested && !rec.collected {
			remoteBytes += int64(rec.bytes)
		}
	}
	ownPressure := a.spent+a.queuedBytes >= prototypePhysicalBudget*3/4
	remotePressure := remoteBytes >= prototypePhysicalBudget*3/4
	if ownPressure != remotePressure {
		return remotePressure
	}
	return a.collectRemote
}

func (l *IMAPLink) collectSegments() {
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	next := 0
	for {
		select {
		case <-l.ctx.Done():
			return
		case <-tick.C:
		}
		count := len(l.accounts)
		if count == 0 {
			continue
		}
		// Fairness is among actual requests, not actors which have returned their
		// writers. Rotate the first producer after each existing scheduler tick.
		start := next
		next = (next + 1) % count
		for n := 0; n < count; n++ {
			a := l.accounts[(start+n)%count]
			a.mu.Lock()
			var chosen *cleanupJob
			preferRemote := a.preferRemoteCollectionLocked()
			queue := func(key segmentKey, rec *segmentRecord) {
				if rec.remote && rec.collected {
					select {
					case l.retirements <- Retirement{Session: key.session, Account: a.config.ID, Generation: key.generation, Kind: frameCollected}:
						delete(a.remoteSegments, key)
					default:
					}
					return
				}
				if !rec.retired || rec.queued || rec.appendPending != 0 || len(rec.awaiting) != 0 {
					return
				}
				if l.client {
					select {
					case l.retirements <- Retirement{Session: key.session, Account: a.config.ID, Generation: key.generation, Kind: frameCollect}:
						rec.queued = true
					default:
					}
					return
				}
				if rec.remote && !rec.requested || a.writer == nil || a.parking.Load() || a.reclaiming.Load() || !l.reclaimer.permitted(a, rec.folder) {
					return
				}
				if !rec.remote && (rec.proofUnknown || rec.appends != segmentAppends || rec.validity == 0) {
					return
				}
				if rec.readyAt.IsZero() {
					rec.readyAt = time.Now()
				}
				// Alternate directions when both have eligible records. Age alone
				// lets an earlier upload monopolize collection during a download.
				if chosen != nil {
					if rec.remote != chosen.record.remote {
						if rec.remote != preferRemote {
							return
						}
					} else if !rec.readyAt.Before(chosen.record.readyAt) {
						return
					}
				}
				chosen = &cleanupJob{a, key, rec}
			}
			for key, rec := range a.segments {
				queue(key, rec)
			}
			if !l.client {
				for key, rec := range a.remoteSegments {
					queue(key, rec)
				}
			}
			if chosen != nil {
				select {
				case a.writerOps <- writerOperation{cleanup: chosen}:
					chosen.record.queued = true
				default:
				}
			}
			a.mu.Unlock()
		}
	}
}

// Only the existing server APPEND owner calls this, with all its tagged
// commands drained. The exact consumed UID ownership proof is mandatory.
func (p *mailPipe) reclaim(job cleanupJob, c *ReclamationCoordinator, budgets ...time.Duration) error {
	turnStarted := time.Now()
	rec := job.record
	c.folder(rec.folder)
	uids, ok := rec.uids.Nums()
	if !ok || len(uids) != segmentAppends || rec.validity == 0 {
		return ErrProtocol
	}
	c.stage("select")
	box, err := p.client.Select(rec.folder, nil).Wait()
	if err != nil {
		return err
	}
	if box.UIDValidity != rec.validity {
		return ErrProtocol
	}
	if box.NumMessages == 0 {
		return nil
	}
	if box.NumMessages > segmentAppends {
		return ErrProtocol
	}
	// Keep the amortized path only for a known byte-bounded generation.
	// Incoming proofs have unknown totals, so inventory them before mutation.
	// A large first EXPUNGE can time out and occupy writers on later MULTI
	// runs; never flag an entire unbounded generation to learn that limit.
	// Only a fresh empty SELECT can release its original charge.
	if box.NumMessages == segmentAppends && !rec.refused && rec.bytes > 0 && rec.bytes <= collectionFirstPageBytes {
		p.allowExpunge.Store(true)
		status, err := p.deleteUIDs(rec.uids, rec.folder, c.stage)
		p.allowExpunge.Store(false)
		if err != nil {
			return err
		}
		if status.UIDValidity != rec.validity || status.NumMessages == nil || *status.NumMessages != 0 {
			return ErrProtocol
		}
		return nil
	}
	c.stage("collection_inventory")
	rows, err := p.client.Fetch(imap.SeqSet{{Start: 1, Stop: box.NumMessages}}, &imap.FetchOptions{UID: true, Flags: true, RFC822Size: true}).Collect()
	if err != nil {
		return err
	}
	if len(rows) != int(box.NumMessages) {
		return ErrProtocol
	}
	proof := make(map[imap.UID]bool, len(uids))
	for _, uid := range uids {
		proof[uid] = true
	}
	for _, row := range rows {
		if !proof[row.UID] {
			return ErrProtocol
		}
	}
	pageBytes := int64(collectionFirstPageBytes)
	pageMessages := uint64(collectionPageSize)
	pages := 1
	if rec.refused {
		pageBytes = collectionPageBytes
		pageMessages = collectionRetryMessages
	} else if len(budgets) > 0 {
		pages = 3
	}
	// This retired generation is immutable and its exact UID inventory is
	// bounded by 128 entries. Reuse that inventory only within this writer's
	// mutation lease. Re-fetching it between successful pages needlessly held
	// physical credit across later MULTI runs. A new turn always inventories
	// afresh; every page proves deletion and each turn audits count/validity.
	p.allowExpunge.Store(true)
	defer p.allowExpunge.Store(false)
	auditCount := func(remaining uint32) error {
		c.stage("audit_select")
		status, err := p.inventoryMailbox(rec.folder, &imap.StatusOptions{NumMessages: true, UIDValidity: true})
		if err != nil {
			return err
		}
		if status.UIDValidity != rec.validity || status.NumMessages == nil || *status.NumMessages != remaining {
			return ErrProtocol
		}
		return nil
	}
	for turn := 0; ; turn++ {
		pageStarted := time.Now()
		page, deferred, _, count, err := mutationPageBounded(rows, ^uint32(0), pageBytes, pageMessages)
		if err != nil {
			return err
		}
		c.stage("store_expunge")
		if err := p.clearDeferredDeletion(deferred, c.stage); err != nil {
			return err
		}
		// Between pages, prove the exact deleted UIDs absent before flagging
		// another bounded page. Re-SELECT once at the end of this leased turn;
		// a second count audit between every page adds no storage proof. No
		// charge or peer credit is released before the final empty SELECT.
		if _, err := p.deleteUIDs(page, "", c.stage); err != nil {
			return err
		}
		remainingCount := uint32(uint64(len(rows)) - count)
		if remainingCount == 0 {
			return auditCount(0)
		}
		c.stage("collection_page_audit")
		left, err := p.client.Fetch(page, &imap.FetchOptions{UID: true}).Collect()
		if err != nil {
			return err
		}
		if len(left) != 0 {
			return ErrProtocol
		}
		if turn+1 >= pages || time.Since(turnStarted)+2*time.Since(pageStarted) >= budgets[0] {
			if err := auditCount(remainingCount); err != nil {
				return err
			}
			return errCollectionPartial
		}
		remaining := rows[:0]
		for _, row := range rows {
			if !page.Contains(row.UID) {
				// Deferred deletion flags were cleared before the completed
				// EXPUNGE; do not repeat that STORE on this same inventory.
				flags := row.Flags[:0]
				for _, flag := range row.Flags {
					if flag != imap.FlagDeleted {
						flags = append(flags, flag)
					}
				}
				row.Flags = flags
				remaining = append(remaining, row)
			}
		}
		rows = remaining
	}
}

func (a *mailAccountState) finishCollection(job cleanupJob) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.collectRemote = !job.record.remote
	if job.record.remote {
		if a.remoteSegments[job.key] != job.record {
			return nil
		}
		job.record.collected = true
		job.record.queued = false
		return nil
	}
	if a.segments[job.key] != job.record {
		return nil
	}
	if err := a.refundLocked(job.record.bytes); err != nil {
		return err
	}
	delete(a.segments, job.key)
	a.retired.Add(1)
	a.bulkBlocked = false
	return nil
}
