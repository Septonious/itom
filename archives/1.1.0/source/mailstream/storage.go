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
	Kind       byte
}
type StorageLink interface {
	Retirements() <-chan Retirement
	Acknowledge(SessionID, uint64)
	ConfirmRetired(Retirement) error
	RequestCollection(Retirement) error
	ConfirmCollection(Retirement) error
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
	remote                   bool
	deleted                  bool
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

// Only the server deletes mail. A producer's authenticated collection request
// proves its APPEND tags are terminal; the server independently verifies its
// own reader has drained and unselected that exact inbound generation.
func (l *IMAPLink) RequestCollection(r Retirement) error {
	if l.client {
		return ErrProtocol
	}
	for _, a := range l.accounts {
		if a.config.ID != r.Account {
			continue
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.id != r.Session || a.data == nil || a.data.generationDebug.Load() <= r.Generation {
			return ErrProtocol
		}
		key := segmentKey{r.Session, r.Generation}
		if a.remoteSegments[key] != nil {
			return nil
		}
		if len(a.remoteSegments) >= 128 {
			return ErrCapacity
		}
		a.remoteSegments[key] = &segmentRecord{folder: l.segmentFolder(r.Session, false, r.Generation), remote: true, retired: true}
		return nil
	}
	return ErrProtocol
}
func (l *IMAPLink) ConfirmCollection(r Retirement) error {
	if !l.client {
		return ErrProtocol
	}
	for _, a := range l.accounts {
		if a.config.ID != r.Account {
			continue
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		key := segmentKey{r.Session, r.Generation}
		record := a.segments[key]
		if record == nil {
			return nil
		} // repeated authenticated confirmation
		if !record.queued || !record.retired || record.appendPending != 0 || len(record.awaiting) != 0 {
			return ErrProtocol
		}
		a.spent -= record.bytes
		delete(a.segments, key)
		a.retired.Add(1)
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
			queue := func(key segmentKey, record *segmentRecord) {
				if record.remote && record.deleted {
					select {
					case l.retirements <- Retirement{key.session, a.config.ID, key.generation, frameCollected}:
						delete(a.remoteSegments, key)
					default:
					}
					return
				}
				if !record.retired || len(record.awaiting) != 0 || record.appendPending != 0 || record.queued || record.refused {
					return
				}
				record.queued = true
				if l.client {
					select {
					case l.retirements <- Retirement{key.session, a.config.ID, key.generation, frameCollect}:
					default:
						record.queued = false
					}
					return
				}
				job := cleanupJob{a, key, record}
				err := a.config.Maintenance.submit(maintenanceJob{ctx: a.ctx, run: func(p *mailPipe) error { return p.cleanup(job) }, failed: func(error) {
					a.mu.Lock()
					record.refused = true
					a.mu.Unlock()
				}})
				if errors.Is(err, ErrCapacity) {
					record.queued = false
				} else if err != nil {
					record.refused = true
				}
			}
			for key, record := range a.segments {
				queue(key, record)
			}
			for key, record := range a.remoteSegments {
				queue(key, record)
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
	if job.record.folder != a.link.segmentFolder(job.key.session, !job.record.remote, job.key.generation) {
		return ErrProtocol
	}
	err := p.client.Delete(job.record.folder).Wait()
	var tagged *imap.Error
	if err == nil || errors.As(err, &tagged) {
		p.phase.Store(5)
		var listErr error
		rows, readErr := p.client.List("", job.record.folder, nil).Collect()
		listErr = readErr
		if listErr != nil {
			err = listErr
		} else if len(rows) != 0 {
			err = ErrProtocol
		} else {
			err = nil
		}
	}
	a.mu.Lock()
	if err == nil {
		records := a.segments
		if job.record.remote {
			records = a.remoteSegments
		}
		if current := records[job.key]; current != job.record {
			a.mu.Unlock()
			return ErrProtocol
		}
		if !job.record.remote {
			a.spent -= job.record.bytes
			a.retired.Add(1)
			delete(records, job.key)
		} else {
			job.record.deleted = true
		}
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
		if _, err := p.selectLog(a.link.segmentFolder(id, false, generation), false); err != nil {
			return err
		}
		p.generationDebug.Store(generation)
		select {
		case a.link.retirements <- Retirement{Session: id, Account: a.config.ID, Generation: old}:
			return nil
		case <-p.done:
			return ErrClosed
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
