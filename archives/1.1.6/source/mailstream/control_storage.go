package mailstream

import (
	"context"
	"encoding/binary"
	"io"
	"math"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

const controlBatch = 32

type uidWatermark struct{ Validity, UID uint32 }
type controlWrite struct {
	validity uint32
	bytes    int
}
type retentionMaintenance struct {
	initialMessages                                uint32
	pendingMessages                                int
	controlBytes, incomingBytes                    int
	reads                                          map[imap.UID]controlWrite
	requested                                      bool
	collectControlOut                              bool
	initialOut, remoteRead, remoteStartOut         uidWatermark
	clearedIn, clearedOut, deleted, ackSent        uidWatermark
	writes                                         map[imap.UID]controlWrite
	pending                                        bool
	cancel                                         context.CancelFunc
	lastAttempt, lastIdle                          time.Time
	epoch, reportEpoch                             SessionID
	lastReport                                     time.Time
	reportUID                                      uint32
	recovered, recoveryReady, recoverySent         bool
	peerPressure, pressureSent, advertisedPressure bool
	pressureVersion, peerPressureVersion           uint64
	folders                                        []string
	folderIndex                                    int
	inventoried                                    bool
	boundaryOut                                    uidWatermark
	failure                                        string
	emptiedFolders, controlMessages                uint64
}

type maintenanceLink interface {
	maintenanceFrames(SessionID) []frame
	applyMaintenance(SessionID, frame) error
}

func (a *mailAccountState) beginEpoch(id SessionID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	m := &a.maintenance
	if m.epoch == id {
		return
	}
	m.epoch = id
	m.folders = nil
	m.folderIndex = 0
	m.inventoried = false
	m.boundaryOut = uidWatermark{}
	m.remoteRead = uidWatermark{}
	m.remoteStartOut = uidWatermark{}
	m.recoveryReady = false
	m.recoverySent = false
	m.recovered = false
	m.peerPressure, m.pressureSent, m.advertisedPressure = false, false, false
	m.pressureVersion, m.peerPressureVersion = 0, 0
	m.reportEpoch = SessionID{}
	m.lastIdle = time.Time{}
}

func putWatermark(dst []byte, v uidWatermark) []byte {
	dst = binary.BigEndian.AppendUint32(dst, v.Validity)
	return binary.BigEndian.AppendUint32(dst, v.UID)
}
func readWatermark(p []byte) uidWatermark {
	return uidWatermark{binary.BigEndian.Uint32(p), binary.BigEndian.Uint32(p[4:])}
}
func covers(proof, target uidWatermark) bool {
	return proof.Validity != 0 && proof.Validity == target.Validity && proof.UID >= target.UID
}

func (l *IMAPLink) maintenanceFrames(id SessionID) []frame {
	l.mu.Lock()
	current := l.attached
	l.mu.Unlock()
	if current != id {
		return nil
	}
	var frames []frame
	for _, a := range l.accounts {
		a.mu.Lock()
		m := &a.maintenance
		if l.client && m.epoch != id {
			m.epoch = id
			m.recovered = false
			m.peerPressure, m.pressureSent, m.advertisedPressure = false, false, false
			m.pressureVersion, m.peerPressureVersion = 0, 0
			m.reportEpoch = SessionID{}
		}
		if a.failed || a.control == nil || m.epoch != id {
			a.mu.Unlock()
			continue
		}
		// Pressure requests work, never a capacity refund. Historical debt remains
		// charged until the peer returns an audited recovery proof.
		pressure := a.storagePressureLocked()
		if pressure && !m.pressureSent || m.pressureSent && m.advertisedPressure != pressure {
			value := byte(0)
			if pressure {
				value = 1
			}
			m.pressureVersion++
			frames = append(frames, frame{kind: frameStoragePressure, offset: m.pressureVersion, data: append([]byte(a.config.ID), value)})
			m.pressureSent, m.advertisedPressure = true, pressure
		}
		if l.client {
			cursor := a.control.cursor.Load()
			fresh := m.reportEpoch != id
			delta := cursor - m.reportUID
			if fresh || delta >= controlBatch || !m.recovered && a.historyRequiredLocked() && cursor != m.reportUID && time.Since(m.lastReport) >= time.Second {
				data := putWatermark([]byte(a.config.ID), uidWatermark{a.control.validity, cursor})
				data = putWatermark(data, m.initialOut)
				frames = append(frames, frame{kind: frameControlRead, data: data})
				m.reportUID = cursor
				m.reportEpoch = id
				m.lastReport = time.Now()
			}
		} else {
			if m.clearedIn.Validity != 0 && m.clearedIn != m.ackSent {
				frames = append(frames, frame{kind: frameControlCollected, data: putWatermark([]byte(a.config.ID), m.clearedIn)})
				m.ackSent = m.clearedIn
			}
			if m.recoveryReady && !m.recoverySent {
				frames = append(frames, frame{kind: frameStorageRecovered, data: []byte(a.config.ID)})
				m.recoverySent = true
			}
		}
		a.mu.Unlock()
	}
	return frames
}
func (l *IMAPLink) applyMaintenance(id SessionID, f frame) error {
	extra := 0
	switch f.kind {
	case frameControlRead:
		if l.client {
			return ErrProtocol
		}
		extra = 16
	case frameControlCollected:
		if !l.client {
			return ErrProtocol
		}
		extra = 8
	case frameStoragePressure:
		extra = 1
		if f.offset == 0 || len(f.data) == 0 || f.data[len(f.data)-1] > 1 {
			return ErrProtocol
		}
	case frameStorageRecovered:
		if !l.client {
			return ErrProtocol
		}
	default:
		return ErrProtocol
	}
	if f.stream != 0 || f.kind != frameStoragePressure && f.offset != 0 || len(f.data) <= extra || !validIdentity(string(f.data[:len(f.data)-extra])) {
		return ErrProtocol
	}
	a := l.account(string(f.data[:len(f.data)-extra]))
	if a == nil {
		return ErrProtocol
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	m := &a.maintenance
	if m.epoch != id {
		return ErrCapacity
	} // Role attachment can finish after the first authenticated update.
	switch f.kind {
	case frameStoragePressure:
		if f.offset <= m.peerPressureVersion {
			return nil
		}
		m.peerPressureVersion = f.offset
		m.peerPressure = f.data[len(f.data)-1] == 1
	case frameControlRead:
		read, start := readWatermark(f.data[len(f.data)-16:]), readWatermark(f.data[len(f.data)-8:])
		if read.Validity == 0 || start.Validity == 0 {
			return ErrProtocol
		}
		if m.remoteRead.Validity != 0 && (m.remoteRead.Validity != read.Validity) {
			return ErrProtocol
		}
		if m.remoteStartOut.Validity != 0 && m.remoteStartOut != start {
			return ErrProtocol
		}
		if read.UID < m.remoteRead.UID {
			return nil
		}
		m.remoteRead = read
		m.remoteStartOut = start
	case frameControlCollected:
		mark := readWatermark(f.data[len(f.data)-8:])
		if mark.Validity != m.initialOut.Validity {
			return ErrProtocol
		}
		if m.deleted.Validity != 0 && mark.UID < m.deleted.UID {
			return nil
		}
		if err := a.refundControlLocked(mark); err != nil {
			return err
		}
	case frameStorageRecovered:
		if !m.recovered {
			if err := a.refundLocked(a.recovered); err != nil {
				return err
			}
			a.recovered = 0
			m.recovered = true
		}
	}
	return nil
}
func (a *mailAccountState) controlAppended(result *imap.AppendData, n int) {
	if result == nil || result.UID == 0 || result.UIDValidity == 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	m := &a.maintenance
	if m.pendingMessages > 0 {
		m.pendingMessages--
	}
	if covers(m.deleted, uidWatermark{result.UIDValidity, uint32(result.UID)}) {
		if err := a.refundLocked(n); err != nil {
			a.recoveryFailure = "CONTROL_REFUND_FAILED"
		}
		return
	}
	if m.writes == nil {
		m.writes = make(map[imap.UID]controlWrite)
	}
	if old, ok := m.writes[result.UID]; ok {
		m.controlBytes -= old.bytes
	}
	m.writes[result.UID] = controlWrite{result.UIDValidity, n}
	m.controlBytes += n
}
func (a *mailAccountState) refundControlLocked(mark uidWatermark) error {
	n := 0
	for uid, w := range a.maintenance.writes {
		if w.validity == mark.Validity && uint32(uid) <= mark.UID {
			n += w.bytes
		}
	}
	if err := a.refundLocked(n); err != nil {
		return err
	}
	for uid, w := range a.maintenance.writes {
		if w.validity == mark.Validity && uint32(uid) <= mark.UID {
			a.maintenance.controlBytes -= w.bytes
			delete(a.maintenance.writes, uid)
		}
	}
	initial := a.maintenance.initialOut
	if mark.Validity == initial.Validity {
		// Remaining initial messages occupy distinct UIDs above the audited
		// fence. The UID span is a safe upper bound even when there are holes;
		// unlike subtracting an unproved count, it cannot invent deletion.
		remaining := uint32(0)
		if mark.UID < initial.UID {
			remaining = initial.UID - mark.UID
		}
		a.maintenance.initialMessages = min(a.maintenance.initialMessages, remaining)
	}
	a.maintenance.deleted = mark
	return nil
}

// UID FETCH does not depend on sequence positions, which change after EXPUNGE.
// Polls use an absolute forward UID range; no reversed n:* range re-reads the tail.
func (p *mailPipe) receiveControl(ctx context.Context, out chan<- []byte) error {
	for {
		p.phase.Store(11)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.done:
			return ErrClosed
		case cmd := <-p.commands:
			if err := p.runCommand(cmd); err != nil {
				return err
			}
		default:
		}
		if p.after == math.MaxUint32 {
			return ErrCapacity
		}
		// The upper bound is a literal maximum UID, never '*': n:* reverses
		// when n is beyond the tail and can re-read an old message. A forward
		// range crosses UID holes without STATUS on the selected mailbox or an
		// extra round trip. FETCH streams its finite current snapshot; per-mail
		// and actor byte limits still bound buffering.
		end := uint32(math.MaxUint32)
		before := p.after
		fetchStarted := time.Now()
		command := p.client.Fetch(imap.UIDSet{{Start: imap.UID(p.after + 1), Stop: imap.UID(end)}}, &imap.FetchOptions{UID: true, RFC822Size: true, BodySection: []*imap.FetchItemBodySection{{Peek: true}}})
		for row := command.Next(); row != nil; row = command.Next() {
			var uid imap.UID
			size := int64(-1)
			var raw []byte
			for item := row.Next(); item != nil; item = row.Next() {
				switch v := item.(type) {
				case imapclient.FetchItemDataUID:
					uid = v.UID
				case imapclient.FetchItemDataRFC822Size:
					size = v.Size
				case imapclient.FetchItemDataBodySection:
					if raw != nil || v.Literal == nil {
						p.conn.NetConn().Close()
						command.Close()
						return ErrProtocol
					}
					var err error
					raw, err = io.ReadAll(io.LimitReader(v.Literal, 2*maxMailPlain+1))
					if err != nil || len(raw) > 2*maxMailPlain {
						p.conn.NetConn().Close()
						command.Close()
						return ErrCapacity
					}
				}
			}
			if uint32(uid) <= p.after || uint32(uid) > end || raw == nil || int64(len(raw)) != size {
				p.conn.NetConn().Close()
				command.Close()
				return ErrProtocol
			}
			packets, err := decodeMail(raw)
			if err != nil {
				if p.onControlConsumed != nil {
					p.onControlConsumed(uid, p.validity, len(raw))
				}
				p.after = uint32(uid)
				p.cursor.Store(p.after)
				continue
			}
			for _, body := range packets {
				if p.onPacket != nil {
					p.onPacket(body)
				}
				select {
				case out <- body:
				case <-ctx.Done():
					p.conn.NetConn().Close()
					command.Close()
					return ctx.Err()
				case <-p.done:
					command.Close()
					return ErrClosed
				}
			}
			if p.onControlConsumed != nil {
				p.onControlConsumed(uid, p.validity, len(raw))
			}
			p.after = uint32(uid)
			p.cursor.Store(p.after)
		}
		if err := command.Close(); err != nil {
			return ErrClosed
		}
		if p.after == before {
			// The empty FETCH is itself a direct IMAP command sample.
			// A separate periodic NOOP delays the receive critical path.
			p.observeEmptyControlFetch(fetchStarted)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-p.done:
				return ErrClosed
			case cmd := <-p.commands:
				if err := p.runCommand(cmd); err != nil {
					return err
				}
			case <-time.After(time.Millisecond):
			}
		}
	}
}

// Incoming control occupies the peer's reserve, not this endpoint's DATA debt.
// Capture exact consumed MIME bytes so pressure is measured in both directions.
func (a *mailAccountState) controlConsumed(uid imap.UID, validity uint32, n int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	m := &a.maintenance
	if m.reads == nil {
		m.reads = make(map[imap.UID]controlWrite)
	}
	if old, ok := m.reads[uid]; ok {
		m.incomingBytes -= old.bytes
	}
	m.reads[uid] = controlWrite{validity, n}
	m.incomingBytes += n
}
func (a *mailAccountState) controlInputCollectedLocked(mark uidWatermark) {
	m := &a.maintenance
	for uid, w := range m.reads {
		if w.validity == mark.Validity && uint32(uid) <= mark.UID {
			m.incomingBytes -= w.bytes
			delete(m.reads, uid)
		}
	}
}

// Substantial inherited debt requests proactive recovery before bulk exhausts
// the account. Deferral never changes the persisted debt or hard byte cap.
func (a *mailAccountState) storagePressureLocked() bool {
	// Reconcile substantial inherited DATA debt while the other three quarters
	// of the account can still carry traffic. Waiting until 75% occupancy makes
	// a short first upload exhaust accounts before historical work can finish.
	// Small crash reservations still avoid an unnecessary namespace sweep.
	if a.recovered >= prototypePhysicalBudget/4 {
		return true
	}
	if a.spent+a.queuedBytes < prototypePhysicalBudget*3/4 {
		return false
	}
	// Current-session DATA has its own exact retirement/collection path.
	// Its backlog must not trigger an unrelated historical namespace sweep.
	if a.recovered > 0 {
		return true
	}
	for key, rec := range a.segments {
		if key.session != a.maintenance.epoch && rec.bytes > 0 {
			return true
		}
	}
	return false
}
func (a *mailAccountState) historyRequiredLocked() bool {
	return a.storagePressureLocked() || a.maintenance.peerPressure
}
