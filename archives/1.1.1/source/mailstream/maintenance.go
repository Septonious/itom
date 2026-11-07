package mailstream

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

func (c *ReclamationCoordinator) folder(s string) { c.mu.Lock(); c.state.Folder = s; c.mu.Unlock() }
func (c *ReclamationCoordinator) sessionStarted(l *IMAPLink) {
	c.mu.Lock()
	if c.sessions == nil {
		c.sessions = map[*IMAPLink]bool{}
	}
	c.sessions[l] = true
	stop := c.idleStop
	c.mu.Unlock()
	if stop != nil {
		stop()
	}
}
func (c *ReclamationCoordinator) sessionEnded(l *IMAPLink) {
	c.mu.Lock()
	delete(c.sessions, l)
	if len(c.sessions) == 0 {
		c.quietSince = time.Now()
	}
	c.mu.Unlock()
}

func nonexistent(e error) bool {
	var status *imap.Error
	return errors.As(e, &status) && status.Code == imap.ResponseCodeNonExistent
}

// A collection turn owns at most one bounded prefix. Provider mutation service
// time need not resemble NOOP latency; a busy folder must not turn every retry
// into the same unbounded STORE. Only audited UIDs advance the credit fence.
const collectionPageSize = 256

// Slow or uncertain mutations also need a message bound: many tiny control
// records can exceed provider service time while staying far below a byte cap.
// Only accounts that have encountered a collection failure take this path.
const collectionRetryMessages = 32
const collectionTimeout = 8 * time.Second

// A DATA generation can contain tens of MiB even with only 128 messages.
// Bound retry/historical mutation work by actual MIME bytes, independently
// of the UID page cap. Healthy complete DATA keeps its amortized fast path.
const collectionPageBytes = 4 << 20

// A first complete DATA generation may exceed 40 MiB. Bound its first
// mutation as well, while amortizing metadata on providers that accept it.
// An actual refusal/unknown outcome keeps the established four-MiB retry cap.
const collectionFirstPageBytes = 16 << 20

func mutationPage(rows []*imapclient.FetchMessageBuffer, fence uint32, messageLimits ...uint64) (owned, deferred imap.UIDSet, end uint32, count uint64, err error) {
	return mutationPageBounded(rows, fence, collectionPageBytes, messageLimits...)
}

func mutationPageBounded(rows []*imapclient.FetchMessageBuffer, fence uint32, byteLimit int64, messageLimits ...uint64) (owned, deferred imap.UIDSet, end uint32, count uint64, err error) {
	sort.Slice(rows, func(i, j int) bool { return rows[i].UID < rows[j].UID })
	var last imap.UID
	var bytes int64
	full, skipped := false, false
	end = fence
	for _, row := range rows {
		if row.UID == 0 || row.UID == last || row.RFC822Size < 0 {
			return nil, nil, 0, 0, ErrProtocol
		}
		last = row.UID
		if uint32(row.UID) > fence {
			continue
		}
		if count > 0 && (row.RFC822Size > byteLimit-bytes || len(messageLimits) > 0 && count >= messageLimits[0]) {
			full = true
		}
		if !full {
			owned.AddNum(row.UID)
			bytes += row.RFC822Size
			count++
			end = uint32(row.UID)
			continue
		}
		skipped = true
		// An older timed-out attempt may have flagged the entire generation.
		// Clear only proved-owned later UIDs before ordinary EXPUNGE so this
		// turn cannot silently expand back into the original huge deletion.
		for _, flag := range row.Flags {
			if strings.EqualFold(string(flag), string(imap.FlagDeleted)) {
				deferred.AddNum(row.UID)
				break
			}
		}
	}
	if !skipped {
		end = fence
	}
	return
}

func (p *mailPipe) clearDeferredDeletion(uids imap.UIDSet, stage func(string)) error {
	if len(uids) == 0 {
		return nil
	}
	stage("clear_deferred_flags")
	return p.client.Store(uids, &imap.StoreFlags{Op: imap.StoreFlagsDel, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close()
}

func (p *mailPipe) collectPrefix(c *ReclamationCoordinator, folder string, target uidWatermark, whole bool, messageLimits ...uint64) (uidWatermark, uint64, bool, error) {
	c.folder(folder)
	c.stage("maintenance_select")
	selected, e := p.client.Select(folder, nil).Wait()
	if e != nil {
		if whole && nonexistent(e) {
			return target, 0, true, nil
		}
		return uidWatermark{}, 0, false, e
	}
	if target.Validity != 0 && target.Validity != selected.UIDValidity {
		return uidWatermark{}, 0, false, ErrProtocol
	}
	target.Validity = selected.UIDValidity
	if whole {
		if selected.UIDNext == 0 {
			return uidWatermark{}, 0, false, ErrProtocol
		}
		target.UID = uint32(selected.UIDNext) - 1
	}
	requested := target.UID
	if target.UID == 0 || selected.NumMessages == 0 && uint32(selected.UIDNext) > target.UID {
		return target, 0, true, nil
	}
	if selected.NumMessages == 0 {
		return uidWatermark{}, 0, false, ErrProtocol
	}
	c.stage("maintenance_inventory")
	// Sequence order is UID order. New messages may arrive above the frozen UID
	// fence; they are inventoried but never marked deleted or acknowledged here.
	count := min(uint32(collectionPageSize), selected.NumMessages)
	rows, e := p.client.Fetch(imap.SeqSet{{Start: 1, Stop: count}}, &imap.FetchOptions{UID: true, Flags: true, RFC822Size: true}).Collect()
	if e != nil {
		return uidWatermark{}, 0, false, e
	}
	if uint32(len(rows)) > count {
		return uidWatermark{}, 0, false, ErrProtocol
	}
	var last imap.UID
	for _, row := range rows {
		if row.UID == 0 {
			return uidWatermark{}, 0, false, ErrProtocol
		}
		last = max(last, row.UID)
	}
	if selected.NumMessages > count && last != 0 && uint32(last) < target.UID {
		target.UID = uint32(last)
	}
	owned, deferred, fence, removed, e := mutationPage(rows, target.UID, messageLimits...)
	if e != nil {
		return uidWatermark{}, 0, false, e
	}
	target.UID = fence
	if removed > 0 {
		p.allowExpunge.Store(true)
		e = p.clearDeferredDeletion(deferred, c.stage)
		if e == nil {
			_, e = p.deleteUIDs(owned, "", c.stage)
		}
		p.allowExpunge.Store(false)
		if e != nil {
			return uidWatermark{}, 0, false, e
		}
	}
	c.stage("maintenance_audit")
	set := imap.UIDSet{{Start: 1, Stop: imap.UID(target.UID)}}
	remaining, e := p.client.Fetch(set, &imap.FetchOptions{UID: true}).Collect()
	if e != nil {
		return uidWatermark{}, 0, false, e
	}
	if len(remaining) != 0 {
		return uidWatermark{}, 0, false, ErrProtocol
	}
	c.stage("audit_select")
	status, e := p.inventoryMailbox(folder, &imap.StatusOptions{UIDValidity: true, NumMessages: true})
	if e != nil {
		return uidWatermark{}, 0, false, e
	}
	if status.UIDValidity != target.Validity || status.NumMessages == nil {
		return uidWatermark{}, 0, false, ErrProtocol
	}
	complete := target.UID == requested
	if whole {
		complete = *status.NumMessages == 0
	}
	return target, removed, complete, nil
}

func (a *mailAccountState) maintenanceStep(ctx context.Context, p *mailPipe, idle bool) (string, error) {
	l, c := a.link, a.link.reclaimer
	l.mu.Lock()
	id := l.attached
	l.mu.Unlock()
	if idle && id != (SessionID{}) {
		return "", context.Canceled
	}
	a.mu.Lock()
	m := &a.maintenance
	if !idle && m.epoch != id {
		a.mu.Unlock()
		return "", context.Canceled
	}
	if idle && m.epoch != (SessionID{}) {
		m.epoch = SessionID{}
		m.inventoried = false
		m.folders = nil
		m.folderIndex = 0
		m.recoveryReady = false
		m.boundaryOut = uidWatermark{}
	}
	cursor := uidWatermark{}
	if a.control != nil {
		cursor = uidWatermark{a.control.validity, a.control.cursor.Load()}
	}
	remote, start, cin, cout, boundary := m.remoteRead, m.remoteStartOut, m.clearedIn, m.clearedOut, m.boundaryOut
	historical := idle || a.historyRequiredLocked()
	recovering := historical && !m.recoveryReady
	pageMessages := uint64(collectionPageSize)
	if a.collectionPaged {
		pageMessages = collectionRetryMessages
	}
	a.mu.Unlock()
	if idle {
		l.mu.Lock()
		generation := l.handshakeGeneration
		quiet := len(l.handshakes) == 0 && l.attached == (SessionID{})
		l.mu.Unlock()
		if quiet {
			status, err := p.inventoryMailbox(l.controlFolder(true), &imap.StatusOptions{UIDNext: true, UIDValidity: true})
			if err != nil {
				return l.controlFolder(true), err
			}
			l.mu.Lock()
			stillQuiet := l.handshakeGeneration == generation && len(l.handshakes) == 0 && l.attached == (SessionID{})
			l.mu.Unlock()
			if stillQuiet && status.UIDNext != 0 && status.UIDValidity != 0 {
				remote = uidWatermark{status.UIDValidity, uint32(status.UIDNext) - 1}
				recovering = true
			}
		}
	}
	retry := c.retryFolder(a)
	a.mu.Lock()
	if retry != "" {
		a.collectionPaged = true
		pageMessages = collectionRetryMessages
	}
	inventoried := m.inventoried
	a.mu.Unlock()
	if historical && !inventoried && retry == "" {
		c.folder("")
		c.stage("maintenance_list")
		folders, e := p.client.List("", l.folderPrefix()+"-*", nil).Collect()
		if e != nil {
			return "", e
		}
		var owned []string
		for _, f := range folders {
			if l.obsoleteFolder(f.Mailbox, id) {
				owned = append(owned, f.Mailbox)
			}
		}
		if len(owned) > 16384 {
			return "", ErrCapacity
		}
		status, e := p.inventoryMailbox(l.controlFolder(true), &imap.StatusOptions{UIDNext: true, UIDValidity: true})
		if e != nil {
			return "", e
		}
		if status.UIDNext == 0 || status.UIDValidity == 0 {
			return "", ErrProtocol
		}
		a.mu.Lock()
		m.folders = owned
		m.folderIndex = 0
		m.inventoried = true
		m.boundaryOut = uidWatermark{status.UIDValidity, uint32(status.UIDNext) - 1}
		a.mu.Unlock()
		return "", nil
	}
	a.mu.Lock()
	inventoryComplete := m.inventoried && m.folderIndex == len(m.folders)
	a.mu.Unlock()
	if historical && !idle && inventoryComplete && start.Validity != 0 && covers(cin, start) && covers(cout, boundary) {
		a.mu.Lock()
		if !m.recoveryReady {
			refund := a.recovered
			for key, r := range a.segments {
				if key.session != id {
					refund += r.bytes
				}
			}
			if e := a.refundLocked(refund); e != nil {
				a.mu.Unlock()
				return "", e
			}
			a.recovered = 0
			for key := range a.segments {
				if key.session != id {
					delete(a.segments, key)
				}
			}
			for key := range a.remoteSegments {
				if key.session != id {
					delete(a.remoteSegments, key)
				}
			}
			m.recoveryReady = true
		}
		a.mu.Unlock()
	}
	a.mu.Lock()
	preferOut := m.collectControlOut
	a.mu.Unlock()
	eligibleIn := cursor.Validity != 0 && (retry == l.controlFolder(false) || retry == "" && (cursor.UID-cin.UID >= controlBatch || idle && cursor.UID > cin.UID || recovering && !covers(cin, start) && start.Validity != 0))
	eligibleOut := remote.Validity != 0 && (retry == l.controlFolder(true) || retry == "" && (remote.UID-cout.UID >= controlBatch || idle && remote.UID > cout.UID || recovering && !covers(cout, boundary)))
	inputTarget, outputTarget := cursor, remote
	if recovering && !idle {
		// Finish the frozen pre-session scope before chasing live control tails.
		// An authenticated consumption watermark remains the deletion ceiling.
		eligibleIn = start.Validity != 0 && cursor.Validity == start.Validity && !covers(cin, start) && (cursor.Validity != cin.Validity || cursor.UID > cin.UID)
		eligibleOut = boundary.Validity != 0 && remote.Validity == boundary.Validity && !covers(cout, boundary) && (remote.Validity != cout.Validity || remote.UID > cout.UID)
		if eligibleIn {
			inputTarget.UID = min(cursor.UID, start.UID)
		}
		if eligibleOut {
			outputTarget.UID = min(remote.UID, boundary.UID)
		}
	}

	if eligibleIn && (!eligibleOut || !preferOut || retry == l.controlFolder(false)) {
		proof, n, _, e := p.collectPrefix(c, l.controlFolder(false), inputTarget, false, pageMessages)
		if e != nil {
			return l.controlFolder(false), e
		}
		a.mu.Lock()
		m.collectControlOut = true
		a.controlInputCollectedLocked(proof)
		m.clearedIn = proof
		m.controlMessages += n
		a.mu.Unlock()
		return l.controlFolder(false), nil
	}
	if eligibleOut {
		proof, n, _, e := p.collectPrefix(c, l.controlFolder(true), outputTarget, false, pageMessages)
		if e != nil {
			return l.controlFolder(true), e
		}
		a.mu.Lock()
		e = a.refundControlLocked(proof)
		if e == nil {
			m.collectControlOut = false
			m.clearedOut = proof
			m.controlMessages += n
		}
		a.mu.Unlock()
		return l.controlFolder(true), e
	}
	if historical && retry != "" && retry != l.controlFolder(false) && retry != l.controlFolder(true) && l.obsoleteFolder(retry, id) {
		_, _, _, e := p.collectPrefix(c, retry, uidWatermark{}, true, pageMessages)
		return retry, e
	}
	if retry != "" {
		return "", nil
	}
	a.mu.Lock()
	i := m.folderIndex
	folder := ""
	if historical && i < len(m.folders) {
		folder = m.folders[i]
	}
	a.mu.Unlock()
	if folder != "" {
		_, _, complete, e := p.collectPrefix(c, folder, uidWatermark{}, true, pageMessages)
		if e != nil {
			return folder, e
		}
		a.mu.Lock()
		if complete {
			m.folderIndex++
			m.emptiedFolders++
		}
		a.mu.Unlock()

		return folder, nil
	}

	if idle {
		a.mu.Lock()
		m.lastIdle = time.Now()
		a.mu.Unlock()
	}
	return "", nil
}

func (l *IMAPLink) maintainRetention() {
	if l.client {
		<-l.ctx.Done()
		return
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	next := 0
	for {
		select {
		case <-l.ctx.Done():
			return
		case <-tick.C:
		}
		a := l.accounts[next%len(l.accounts)]
		preferred := l.reclaimer.recoveryCandidate(l)
		if preferred != nil {
			preferred.mu.Lock()
			due := !preferred.maintenance.pending && time.Since(preferred.maintenance.lastAttempt) >= 500*time.Millisecond
			preferred.mu.Unlock()
			if !due {
				preferred = nil
			}
		}
		if preferred != nil {
			a = preferred
		} else {
			next++
		}
		l.mu.Lock()
		id := l.attached
		l.mu.Unlock()
		a.mu.Lock()
		m := &a.maintenance
		ready := a.ready && !a.failed && a.control != nil
		active := id != (SessionID{}) && a.writer != nil && !a.parking.Load()
		due := time.Since(m.lastAttempt) >= 500*time.Millisecond
		if !active {
			due = time.Since(m.lastIdle) >= 30*time.Second
		}
		if active && a.deferMaintenanceLocked() {
			a.mu.Unlock()
			continue
		}
		if ready && !m.pending && due && (!active || a.needsMaintenanceLocked()) && l.reclaimer.permitted(a, "") {
			m.lastAttempt = time.Now()
			if active {
				select {
				case a.writerOps <- writerOperation{maintenance: true}:
					m.pending = true
					m.requested = true
				default:
				}
				a.mu.Unlock()
				continue
			}
			a.mu.Unlock()
			if id == (SessionID{}) {
				l.idleMaintenance(a)
			}
			continue
		}
		a.mu.Unlock()
	}
}
func (l *IMAPLink) idleMaintenance(a *mailAccountState) {
	ctx, cancel := context.WithTimeout(l.ctx, collectionTimeout)
	defer cancel()
	c := l.reclaimer
	if !c.idleLease(a, cancel) {
		return
	}
	defer c.endIdle()
	a.mu.Lock()
	a.maintenance.cancel = cancel
	if !a.maintenance.lastIdle.IsZero() && time.Since(a.maintenance.lastIdle) >= 30*time.Second {
		a.maintenance.inventoried = false
		a.maintenance.folderIndex = 0
	}
	a.mu.Unlock()
	defer func() { a.mu.Lock(); a.maintenance.cancel = nil; a.mu.Unlock() }()
	p, e := openMailPipe(ctx, a.config)
	if e != nil {
		if a.ctx.Err() == nil && !errors.Is(ctx.Err(), context.Canceled) {
			c.folder("")
			c.pause("MAINTENANCE_LOGIN_FAILED", a)
		}
		return
	}
	defer p.close()
	stop := p.limit(collectionTimeout)
	folder, e := a.maintenanceStep(ctx, p, true)
	timely := stop()
	if e == nil && timely {
		c.completed(a, folder)
		a.mu.Lock()
		continuing := a.historyRequiredLocked() && !a.maintenance.recoveryReady
		a.mu.Unlock()
		c.preferRecovery(a, continuing && folder != "")
		return
	}
	if a.ctx.Err() != nil || errors.Is(ctx.Err(), context.Canceled) {
		return
	}
	a.mu.Lock()
	a.collectionPaged = true
	a.mu.Unlock()
	c.pause("MAINTENANCE_FAILED", a)
}

// Live maintenance borrows the existing APPEND actor; it never owns a reader.
func (a *mailAccountState) processMaintenance(writer **mailPipe, completed chan error, inFlight *int, wait *sync.WaitGroup) {
	defer func() { a.mu.Lock(); a.maintenance.pending = false; a.maintenance.cancel = nil; a.mu.Unlock() }()
	c := a.link.reclaimer
	a.mu.Lock()
	deferred := a.deferMaintenanceLocked()
	if deferred {
		a.maintenance.requested = false
	}
	a.mu.Unlock()
	if deferred {
		return
	}
	if *writer == nil || a.parking.Load() || !c.permitted(a, "") {
		return
	}
	a.mu.Lock()
	a.maintenance.requested = false
	a.mu.Unlock()
	ctx, cancel := context.WithCancel(a.ctx)
	defer cancel()
	a.mu.Lock()
	a.maintenance.cancel = cancel
	a.mu.Unlock()
	if !a.borrowWriterForCollection() {
		return
	}
	defer a.returnCollectionWriter()
	p := *writer
	interrupt := context.AfterFunc(ctx, func() { p.conn.NetConn().Close(); p.client.Close() })
	defer interrupt()
	stop := p.limit(10 * time.Second)
	var e error
	for *inFlight > 0 && e == nil {
		select {
		case e = <-completed:
			*inFlight--
		case <-ctx.Done():
			e = ctx.Err()
		}
	}
	drained := stop()
	leased := false
	if e == nil && drained {
		if !c.acquire(a) {
			return
		}
		leased = true
		defer c.release()
	}
	stop = p.limit(collectionTimeout)
	folder := ""
	if e == nil && drained {
		folder, e = a.maintenanceStep(ctx, p, false)
	}
	timely := stop() && drained
	if e == nil && timely {
		c.completed(a, folder)
		a.mu.Lock()
		continuing := a.historyRequiredLocked() && !a.maintenance.recoveryReady
		a.mu.Unlock()
		c.preferRecovery(a, continuing && folder != "")
		return
	}
	if leased && ctx.Err() == nil && !a.parking.Load() {
		reason := "MAINTENANCE_FAILED"
		if !timely {
			reason = "MAINTENANCE_TIMEOUT"
		}
		a.mu.Lock()
		a.collectionPaged = true
		a.mu.Unlock()
		c.pause(reason, a)
	}
	p.close()
	wait.Wait()
	for {
		select {
		case <-completed:
		default:
			goto joined
		}
	}
joined:
	*inFlight = 0
	*writer = nil
	a.mu.Lock()
	a.writer = nil
	a.mu.Unlock()
	if !a.parking.Load() && a.ctx.Err() == nil {
		fresh, err := openMailPipeAttempt(ctx, a.ctx, a.config)
		if err == nil {
			*writer = fresh
			a.mu.Lock()
			a.writer = fresh
			a.mu.Unlock()
		}
	}
	a.link.notify()
}

func (l *IMAPLink) trackHandshake(id SessionID, present bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.handshakes == nil {
		l.handshakes = make(map[SessionID]time.Time)
	}
	if present {
		l.handshakes[id] = time.Now()
	} else {
		delete(l.handshakes, id)
	}
	l.handshakeGeneration++
}

// Avoid borrowing an active writer for a poll with no eligible cleanup work.
// Account.mu is held by the scheduler.
func (a *mailAccountState) needsMaintenanceLocked() bool {
	m := &a.maintenance
	if a.historyRequiredLocked() && !m.recoveryReady {
		return true
	}
	// Small control tails are cheap to retain. Do not interrupt an APPEND
	// actor merely because housekeeping advanced a UID or old folders exist.
	return max(m.controlBytes, m.incomingBytes) >= reservationQuantum/2 || uint64(m.initialMessages)+uint64(len(m.writes)) >= controlBatch || len(m.reads) >= controlBatch
}

// Control tails can wait during a bulk burst when storage has ample headroom.
// DATA collection remains eligible. Capacity-critical historical recovery and real
// retention pressure always override this optimization. Account.mu is held.
func (a *mailAccountState) deferMaintenanceLocked() bool {
	if a.historyRequiredLocked() && !a.maintenance.recoveryReady || max(a.maintenance.controlBytes, a.maintenance.incomingBytes) >= controlCollectionBytes || a.controlMessagePressureLocked() {
		return false
	}
	last := a.link.lastBulk.Load()
	return last != 0 && time.Since(time.Unix(0, last)) < 300*time.Millisecond
}

// Mutation commands have tagged completion fences. Never ask a provider to
// EXPUNGE while its STORE is still pending. STORE may itself emit unsolicited
// FETCH/EXPUNGE; drain its stream before starting the next command.
func (p *mailPipe) deleteUIDs(owned imap.UIDSet, auditFolder string, stages ...func(string)) (*imap.StatusData, error) {
	stage := func(name string) {
		if len(stages) > 0 {
			stages[0](name)
		}
	}
	stage("store_flags")
	store := p.client.Store(owned, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil)
	if err := store.Close(); err != nil {
		return nil, err
	}
	stage("expunge")
	if err := p.client.Expunge().Close(); err != nil {
		return nil, err
	}
	if auditFolder != "" {
		stage("audit_select")
		return p.inventoryMailbox(auditFolder, &imap.StatusOptions{NumMessages: true, UIDValidity: true})
	}
	return nil, nil
}

// STATUS on a selected mailbox is discouraged by IMAP and can block provider
// metadata operations. Re-SELECT after all mutation replies gives authoritative
// count/validity. Only this pipe's owning actor may call this helper.
func (p *mailPipe) inventoryMailbox(folder string, options *imap.StatusOptions) (*imap.StatusData, error) {
	if box := p.client.Mailbox(); box != nil && box.Name == folder {
		d, err := p.client.Select(folder, nil).Wait()
		if err != nil {
			return nil, err
		}
		s := &imap.StatusData{Mailbox: folder, UIDNext: d.UIDNext, UIDValidity: d.UIDValidity}
		if options.NumMessages {
			n := d.NumMessages
			s.NumMessages = &n
		}
		return s, nil
	}
	return p.client.Status(folder, options).Wait()
}
