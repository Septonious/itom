package mailstream

import (
	"context"
	"errors"
	"fmt"
	"time"

	imap "github.com/emersion/go-imap/v2"
)

const segmentAppends = 8

type Retirement struct {
	Session    SessionID
	Account    string
	Generation uint32
}
type StorageLink interface {
	Retirements() <-chan Retirement
	Acknowledge(SessionID, uint64)
	ConfirmRetired(Retirement) error
}
type segmentKey struct {
	session    SessionID
	generation uint32
}
type segmentRecord struct {
	folder                   string
	bytes                    int
	appendPending            int
	awaiting                 map[uint64]bool
	retired, queued, refused bool
}
type cleanupJob struct {
	account *mailAccountState
	key     segmentKey
	record  *segmentRecord
}

func (l *IMAPLink) segmentFolder(id SessionID, out bool, generation uint32) string {
	return fmt.Sprintf("%s-%08x", l.dataFolder(id, out), generation)
}
func (l *IMAPLink) Retirements() <-chan Retirement { return l.retirements }

// These callbacks accept only authenticated current-session delivery and
// retirement frames. They can release only this new Link's exact owned segment
// records. Nothing here opens, changes or refunds a legacy wallet/journal.
func (l *IMAPLink) Acknowledge(id SessionID, sequence uint64) {
	for _, a := range l.accounts {
		a.mu.Lock()
		for key, segment := range a.segments {
			if key.session == id {
				delete(segment.awaiting, sequence)
			}
		}
		a.mu.Unlock()
	}
}
func (l *IMAPLink) ConfirmRetired(r Retirement) error {
	for _, a := range l.accounts {
		if a.config.ID != r.Account {
			continue
		}
		a.mu.Lock()
		if segment := a.segments[segmentKey{r.Session, r.Generation}]; segment != nil {
			segment.retired = true
		}
		a.mu.Unlock()
		return nil
	}
	return ErrProtocol
}

// Maintenance has its own owner and queue. It is never called from OpenTCP,
// Write, Connect or the DATA receive loop. Its finite debt remains charged on
// refusal, timeout, unexpected presence, or an unknown APPEND.
func (l *IMAPLink) collectSegments() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-l.ctx.Done():
			return
		case <-ticker.C:
		}
		for _, a := range l.accounts {
			a.mu.Lock()
			if a.failed || a.control == nil {
				a.mu.Unlock()
				continue
			}
			for key, record := range a.segments {
				if !record.retired || len(record.awaiting) != 0 || record.appendPending != 0 || record.queued || record.refused {
					continue
				}
				record.queued = true
				select {
				case a.control.cleanups <- cleanupJob{a, key, record}:
				default:
					record.queued = false
				}
			}
			a.mu.Unlock()
		}
	}
}

func (p *mailPipe) cleanup(job cleanupJob) error {
	p.phase.Store(4)
	a := job.account
	// The folder is derived from locally charged metadata, never from an
	// untrusted mailbox name supplied by a peer or an arbitrary flags scan.
	if job.record.folder != a.link.segmentFolder(job.key.session, true, job.key.generation) {
		return ErrProtocol
	}
	err := p.client.Delete(job.record.folder).Wait()
	if err == nil {
		p.phase.Store(5)
		var listErr error
		rows, readErr := p.client.List("", job.record.folder, nil).Collect()
		listErr = readErr
		if listErr != nil {
			err = listErr
		} else if len(rows) != 0 {
			err = ErrProtocol
		}
	}
	a.mu.Lock()
	if err == nil {
		if current := a.segments[job.key]; current != job.record {
			a.mu.Unlock()
			return ErrProtocol
		}
		a.spent -= job.record.bytes
		delete(a.segments, job.key)
		a.retired.Add(1)
	} else {
		job.record.refused = true
	}
	a.mu.Unlock()
	if err != nil {
		var status *imap.Error
		if errors.As(err, &status) || errors.Is(err, ErrProtocol) {
			return nil
		} // debt remains; delivery keeps running
		return ErrClosed
	}
	return nil
}

func (a *mailAccountState) configureSegments(p *mailPipe, id SessionID) {
	generation := uint32(0)
	p.rotate = func(ctx context.Context) error {
		if generation == ^uint32(0) {
			return ErrCapacity
		}
		old := generation
		generation++
		p.generation = generation
		p.generationDebug.Store(generation)
		if _, err := p.selectLog(a.link.segmentFolder(id, false, generation), false); err != nil {
			return err
		}
		select {
		case a.link.retirements <- Retirement{Session: id, Account: a.config.ID, Generation: old}:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
