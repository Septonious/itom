package mailstream

import (
	"context"
	"fmt"
	imap "github.com/emersion/go-imap/v2"
	"time"
)

// Two reusable DATA slots per direction/session. A slot cannot be reused until
// its reader has verified exact UID removal and every APPEND/receipt is terminal.
const segmentAppends = 32
const dataSlots uint32 = 2

type Retirement struct {
	Session    SessionID
	Account    string
	Generation uint32
	Kind       byte
}
type StorageLink interface {
	Retirements() <-chan Retirement
	Acknowledge(SessionID, uint64) error
	ConfirmReclaimed(Retirement) error
}
type segmentKey struct {
	session    SessionID
	generation uint32
}
type segmentRecord struct {
	folder               string
	bytes, appendPending int
	awaiting             map[uint64]bool
	reclaimed            bool
}

func (l *IMAPLink) segmentFolder(id SessionID, out bool, generation uint32) string {
	return fmt.Sprintf("%s-%08x", l.dataFolder(id, out), generation%dataSlots)
}
func (l *IMAPLink) Retirements() <-chan Retirement { return l.retirements }
func (l *IMAPLink) Acknowledge(id SessionID, sequence uint64) error {
	return l.releaseDelivery(id, sequence, true)
}
func (l *IMAPLink) Abandon(id SessionID, sequence uint64) {
	if err := l.releaseDelivery(id, sequence, false); err != nil {
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
		for key, record := range a.segments {
			if key.session == id {
				delete(record.awaiting, sequence)
			}
		}
		err := a.releaseReclaimedLocked()
		a.mu.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}
func (a *mailAccountState) releaseReclaimedLocked() error {
	for key, record := range a.segments {
		if !record.reclaimed || record.appendPending != 0 || len(record.awaiting) != 0 {
			continue
		}
		if err := a.refundLocked(record.bytes); err != nil {
			return err
		}
		delete(a.segments, key)
		a.retired.Add(1)
		a.bulkBlocked = false
	}
	return nil
}
func (l *IMAPLink) ConfirmReclaimed(r Retirement) error {
	for _, a := range l.accounts {
		if a.config.ID == r.Account {
			a.mu.Lock()
			defer a.mu.Unlock()
			if record := a.segments[segmentKey{r.Session, r.Generation}]; record != nil {
				record.reclaimed = true
			}
			return a.releaseReclaimedLocked()
		}
	}
	return ErrProtocol
}

// A full DATA slot is consumed by this connection's sole reader. No DELETE,
// maintenance login, or UID EXPUNGE is used during delivery. CLOSE expunges
// the verified, fully consumed slot and leaves this reader unselected. The
// authenticated reclamation receipt is sent only after STATUS proves emptiness.
func (p *mailPipe) reclaimSelected(expected string) error {
	box := p.client.Mailbox()
	if box == nil || box.Name != expected || box.NumMessages != segmentAppends || p.after != segmentAppends {
		return ErrProtocol
	}
	uids, ok := p.consumed.Nums()
	if !ok || len(uids) != segmentAppends {
		return ErrProtocol
	}
	for i, uid := range uids {
		if uid == 0 || i > 0 && uid <= uids[i-1] {
			return ErrProtocol
		}
	}
	stop := p.limit(10 * time.Second)
	defer stop()
	p.phase.Store(7)
	if err := p.client.Store(p.consumed, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); err != nil {
		return err
	}
	p.phase.Store(8)
	if err := p.client.UnselectAndExpunge().Wait(); err != nil {
		return err
	}
	p.phase.Store(9)
	status, err := p.client.Status(expected, &imap.StatusOptions{NumMessages: true, UIDValidity: true}).Wait()
	if err != nil {
		return err
	}
	if status.UIDValidity != p.validity || status.NumMessages == nil || *status.NumMessages != 0 {
		return ErrProtocol
	}
	return nil
}
func (a *mailAccountState) configureSegments(p *mailPipe, id SessionID) {
	generation := uint32(0)
	p.rotate = func(ctx context.Context) error {
		if generation == ^uint32(0) {
			return ErrCapacity
		}
		if err := p.reclaimSelected(a.link.segmentFolder(id, false, generation)); err != nil {
			a.failedStage("data_slot_reclaim", err)
			return err
		}
		old := generation
		generation++
		p.generation = generation
		if _, err := p.selectLog(a.link.segmentFolder(id, false, generation), false); err != nil {
			a.failedStage("data_slot_select", err)
			return err
		}
		p.generationDebug.Store(generation)
		select {
		case a.link.retirements <- Retirement{id, a.config.ID, old, frameReclaimed}:
			return nil
		case <-p.done:
			return ErrClosed
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
