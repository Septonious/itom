package mailstream

import (
	"bytes"
	"context"
	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"sync"
	"testing"
	"time"
)

func seedCollectionFolder(t *testing.T, p *mailPipe, folder string, count int) *segmentRecord {
	t.Helper()
	if err := p.client.Create(folder, nil).Wait(); err != nil {
		t.Fatal(err)
	}
	r := &segmentRecord{folder: folder, queued: true, retired: true}
	raw := bytes.Repeat([]byte{'x'}, 8192)
	for range count {
		cmd, err := p.startAppend(folder, raw)
		if err != nil {
			t.Fatal(err)
		}
		d, err := cmd.Wait()
		if err != nil {
			t.Fatal(err)
		}
		r.uids.AddNum(d.UID)
		r.validity = d.UIDValidity
		r.bytes += len(raw)
	}
	return r
}

// A provider can complete APPEND/NOOP promptly while mutation takes seconds.
// This is a stress hypothesis for the observed timeouts, not a measured Rambler
// command duration. The old two-second actor deadline repeatedly discarded it.
type slowCollectionStore struct{ imapserver.Session }

func (s slowCollectionStore) Store(w *imapserver.FetchWriter, set imap.NumSet, f *imap.StoreFlags, o *imap.StoreOptions) error {
	time.Sleep(2400 * time.Millisecond)
	return s.Session.Store(w, set, f, o)
}

func TestSlowMutationKeepsWriterAndReleasesOnlyAuditedDebt(t *testing.T) {
	accounts, _ := applicationMailServerWrapped(t, true, false, func(s imapserver.Session) imapserver.Session { return slowCollectionStore{s} })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := openMailPipe(ctx, accounts[0])
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if p != nil {
			p.close()
		}
	}()
	original := p
	rec := seedCollectionFolder(t, p, "itom3-slow-data", 128)
	c := NewReclamationCoordinator()
	l := &IMAPLink{ctx: ctx, reclaimer: c, changed: make(chan struct{})}
	key := segmentKey{generation: 0}
	a := &mailAccountState{ctx: ctx, link: l, config: accounts[0], writer: p, segments: map[segmentKey]*segmentRecord{key: rec}, spent: rec.bytes, reserved: rec.bytes}
	var joined sync.WaitGroup
	inFlight := 0
	a.processCleanup(cleanupJob{account: a, key: key, record: rec}, &p, make(chan error, 1), &inFlight, &joined)
	if p != original || c.snapshot().Paused || a.spent != 0 || a.retired.Load() != 1 {
		t.Fatalf("provider mutation budget discarded useful progress: same_writer=%v debt=%d retired=%d reclamation=%+v", p == original, a.spent, a.retired.Load(), c.snapshot())
	}
}
