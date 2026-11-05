package mailstream

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

func (c *ReclamationCoordinator) folder(s string) { c.mu.Lock(); c.state.Folder = s; c.mu.Unlock() }
func (c *ReclamationCoordinator) permitted(a *mailAccountState, f string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.adoptRetryLocked(a)
	return !c.state.Paused || c.failedOwner == a && !time.Now().Before(c.retryAt) && (f == "" || f == c.failedFolder)
}
func (c *ReclamationCoordinator) retryFolder(a *mailAccountState) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.adoptRetryLocked(a)
	if c.state.Paused && c.failedOwner == a {
		return c.failedFolder
	}
	return ""
}
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
func (c *ReclamationCoordinator) idleLease(a *mailAccountState, cancel context.CancelFunc) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.adoptRetryLocked(a)
	if len(c.sessions) != 0 || c.active || !c.quietSince.IsZero() && time.Since(c.quietSince) < 5*time.Second || c.state.Paused && (c.failedOwner != a || time.Now().Before(c.retryAt)) {
		return false
	}
	c.active = true
	c.idleStop = cancel
	return true
}
func (c *ReclamationCoordinator) endIdle() {
	c.mu.Lock()
	c.active = false
	c.idleStop = nil
	c.mu.Unlock()
}

// Only recognized folders in this configured identity's namespace qualify.
func (l *IMAPLink) obsoleteFolder(name string, current SessionID) bool {
	prefix := "itom3-" + l.identity + "-"
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	s := strings.TrimPrefix(name, prefix)
	if s == "control-c2s" || s == "control-s2c" {
		return true
	}
	for _, v := range []string{"1", "2", "3", "4", "5", "6"} {
		if s == "control-v"+v+"-c2s" || s == "control-v"+v+"-s2c" {
			return true
		}
	}
	if len(s) < 36 || s[32] != '-' {
		return false
	}
	raw, e := hex.DecodeString(s[:32])
	if e != nil || len(raw) != 16 {
		return false
	}
	var id SessionID
	copy(id[:], raw)
	if id == current {
		return false
	}
	rest := s[33:]
	for _, d := range []string{"c2s", "s2c"} {
		if rest == d || rest == d+"-park" {
			return true
		}
		if strings.HasPrefix(rest, d+"-") && len(rest) == len(d)+9 {
			_, e := hex.DecodeString(rest[len(d)+1:])
			return e == nil
		}
	}
	return false
}
func nonexistent(e error) bool {
	var status *imap.Error
	return errors.As(e, &status) && status.Code == imap.ResponseCodeNonExistent
}

// Inventory exact UIDs before STORE. Higher control UIDs can arrive concurrently.
func (p *mailPipe) collectPrefix(c *ReclamationCoordinator, folder string, target uidWatermark, whole bool) (uidWatermark, uint64, error) {
	c.folder(folder)
	c.stage("maintenance_select")
	selected, e := p.client.Select(folder, nil).Wait()
	if e != nil {
		if whole && nonexistent(e) {
			return target, 0, nil
		}
		return uidWatermark{}, 0, e
	}
	if target.Validity != 0 && target.Validity != selected.UIDValidity {
		return uidWatermark{}, 0, ErrProtocol
	}
	target.Validity = selected.UIDValidity
	if whole {
		if selected.UIDNext == 0 {
			return uidWatermark{}, 0, ErrProtocol
		}
		target.UID = uint32(selected.UIDNext) - 1
	}
	if target.UID == 0 {
		return target, 0, nil
	}
	set := imap.UIDSet{{Start: 1, Stop: imap.UID(target.UID)}}
	c.stage("maintenance_inventory")
	rows, e := p.client.Fetch(set, &imap.FetchOptions{UID: true, RFC822Size: true}).Collect()
	if e != nil {
		return uidWatermark{}, 0, e
	}
	var owned imap.UIDSet
	for _, r := range rows {
		if r.UID == 0 || uint32(r.UID) > target.UID {
			return uidWatermark{}, 0, ErrProtocol
		}
		owned.AddNum(r.UID)
	}
	if whole && uint32(len(rows)) != selected.NumMessages {
		return uidWatermark{}, 0, ErrProtocol
	}
	if len(rows) > 0 {
		c.stage("maintenance_expunge")
		p.allowExpunge.Store(true)
		_, e = p.deleteUIDs(owned, "")
		p.allowExpunge.Store(false)
		if e != nil {
			return uidWatermark{}, 0, e
		}
	}
	c.stage("maintenance_audit")
	remaining, e := p.client.Fetch(set, &imap.FetchOptions{UID: true}).Collect()
	if e != nil {
		return uidWatermark{}, 0, e
	}
	if len(remaining) > 0 {
		return uidWatermark{}, 0, ErrProtocol
	}
	status, e := p.client.Status(folder, &imap.StatusOptions{UIDValidity: true, NumMessages: true}).Wait()
	if e != nil {
		return uidWatermark{}, 0, e
	}
	if status.UIDValidity != target.Validity || status.NumMessages == nil || whole && *status.NumMessages != 0 {
		return uidWatermark{}, 0, ErrProtocol
	}
	return target, uint64(len(rows)), nil
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
	a.mu.Unlock()
	if idle {
		l.mu.Lock()
		generation := l.handshakeGeneration
		quiet := len(l.handshakes) == 0 && l.attached == (SessionID{})
		l.mu.Unlock()
		if quiet {
			status, err := p.client.Status(l.controlFolder(true), &imap.StatusOptions{UIDNext: true, UIDValidity: true}).Wait()
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
	inventoried := m.inventoried
	a.mu.Unlock()
	if historical && !inventoried && retry == "" {
		c.folder("")
		c.stage("maintenance_list")
		folders, e := p.client.List("", "itom3-"+l.identity+"-*", nil).Collect()
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
		status, e := p.client.Status(l.controlFolder(true), &imap.StatusOptions{UIDNext: true, UIDValidity: true}).Wait()
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
	if eligibleIn && (!eligibleOut || !preferOut || retry == l.controlFolder(false)) {
		proof, n, e := p.collectPrefix(c, l.controlFolder(false), cursor, false)
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
		proof, n, e := p.collectPrefix(c, l.controlFolder(true), remote, false)
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
		_, _, e := p.collectPrefix(c, retry, uidWatermark{}, true)
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
		_, _, e := p.collectPrefix(c, folder, uidWatermark{}, true)
		if e != nil {
			return folder, e
		}
		a.mu.Lock()
		m.folderIndex++
		m.emptiedFolders++
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
		next++
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
	ctx, cancel := context.WithTimeout(l.ctx, 2*time.Second)
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
	stop := p.limit(2 * time.Second)
	folder, e := a.maintenanceStep(ctx, p, true)
	timely := stop()
	if e == nil && timely {
		c.completed(folder)
		return
	}
	if a.ctx.Err() != nil || errors.Is(ctx.Err(), context.Canceled) {
		return
	}
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
	if *writer == nil || a.parking.Load() || !c.permitted(a, "") || !c.acquire(a) {
		return
	}
	defer c.release()
	a.mu.Lock()
	a.maintenance.requested = false
	a.mu.Unlock()
	ctx, cancel := context.WithCancel(a.ctx)
	defer cancel()
	a.mu.Lock()
	a.maintenance.cancel = cancel
	a.mu.Unlock()
	a.reclaiming.Store(true)
	defer a.reclaiming.Store(false)
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
	stop = p.limit(2 * time.Second)
	folder := ""
	if e == nil && drained {
		folder, e = a.maintenanceStep(ctx, p, false)
	}
	timely := stop() && drained
	if e == nil && timely {
		c.completed(folder)
		return
	}
	if ctx.Err() == nil && !a.parking.Load() {
		reason := "MAINTENANCE_FAILED"
		if !timely {
			reason = "MAINTENANCE_TIMEOUT"
		}
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
	return max(m.controlBytes, m.incomingBytes) >= reservationQuantum/2
}

// A stopped endpoint must not permanently own the provider retry circuit.
// Ownership can move only to the same configured identity and account.
func (c *ReclamationCoordinator) adoptRetryLocked(a *mailAccountState) {
	old := c.failedOwner
	if old != nil && old != a && old.ctx.Err() != nil && old.link.identity == a.link.identity && old.config.ID == a.config.ID {
		c.failedOwner = a
	}
}

// Control tails can wait during a bulk burst when storage has ample headroom.
// DATA collection remains eligible. Capacity-critical historical recovery and real
// retention pressure always override this optimization. Account.mu is held.
func (a *mailAccountState) deferMaintenanceLocked() bool {
	if a.historyRequiredLocked() && !a.maintenance.recoveryReady || max(a.maintenance.controlBytes, a.maintenance.incomingBytes) >= reservationQuantum/2 {
		return false
	}
	last := a.link.lastBulk.Load()
	return last != 0 && time.Since(time.Unix(0, last)) < 300*time.Millisecond
}

// deleteUIDs preserves the mutation/audit pipeline without leaving either
// streaming response queue undrained. UID STORE can receive unsolicited FETCH
// and EXPUNGE before its tagged completion. Pipe cancellation/deadline aborts
// both streams; the auxiliary drain always joins, including on an error.
func (p *mailPipe) deleteUIDs(owned imap.UIDSet, auditFolder string) (*imap.StatusData, error) {
	store := p.client.Store(owned, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil)
	storeDone := make(chan error, 1)
	go func() { storeDone <- store.Close() }()
	expunge := p.client.Expunge()
	var audit *imapclient.StatusCommand
	if auditFolder != "" {
		audit = p.client.Status(auditFolder, &imap.StatusOptions{NumMessages: true, UIDValidity: true})
	}
	_, expungeErr := expunge.Collect()
	storeErr := <-storeDone
	if e := errors.Join(storeErr, expungeErr); e != nil {
		return nil, e
	}
	if audit != nil {
		return audit.Wait()
	}
	return nil, nil
}
