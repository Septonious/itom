package mailstream

import (
	"context"
	"sync"
	"testing"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

type messageRetrySession struct{ *byteCollectionSession }

func (s *messageRetrySession) Expunge(w *imapserver.ExpungeWriter, u *imap.UIDSet) error {
	s.fixture.mu.Lock()
	count := len(s.fixture.deleted[s.user+"/"+s.selected])
	s.fixture.mu.Unlock()
	if count > 32 {
		return &imap.Error{Type: imap.StatusResponseTypeNo, Text: "fixture per-message mutation service limit"}
	}
	return s.byteCollectionSession.Expunge(w, u)
}

// Tiny records defeat a byte-only fallback. Old failed flags must be cleared
// outside the next message-bounded page, and credit still requires empty audit.
func TestCollectionRetriesBoundTinyMessagesAfterFailure(t *testing.T) {
	for _, direction := range []string{"control", "data"} {
		t.Run(direction, func(t *testing.T) {
			f := &byteCollectionFixture{sizes: map[string]map[imap.UID]int64{}, deleted: map[string]map[imap.UID]bool{}}
			accounts, _ := applicationMailServerWrapped(t, true, false, func(s imapserver.Session) imapserver.Session {
				return &messageRetrySession{&byteCollectionSession{Session: s, fixture: f}}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			p, e := openMailPipe(ctx, accounts[0])
			if e != nil {
				t.Fatal(e)
			}
			defer func() {
				if p != nil {
					p.close()
				}
			}()
			c := NewReclamationCoordinator()
			l := &IMAPLink{ctx: ctx, login: make(chan struct{}, 4), identity: "message-retry", reclaimer: c, attached: SessionID{1}, changed: make(chan struct{})}
			folder := l.controlFolder(false)
			if direction == "data" {
				folder = l.segmentFolder(SessionID{1}, true, 0)
			}
			rec := seedSizedCollection(t, p, folder, 128)
			if _, e = p.client.Select(folder, nil).Wait(); e != nil {
				t.Fatal(e)
			}
			if e = p.client.Store(rec.uids, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); e != nil {
				t.Fatal(e)
			}
			if direction == "data" {
				rec.refused = true
				for n := 0; n < 4; n++ {
					e = p.reclaim(cleanupJob{record: rec}, c, collectionTimeout)
					if n < 3 && e != errCollectionPartial || n == 3 && e != nil {
						t.Fatalf("tiny-message retry did not make a bounded audited page: turn=%d err=%v", n, e)
					}
				}
				if p.client.Mailbox().NumMessages != 0 {
					t.Fatal("final DATA audit not empty")
				}
				return
			}
			nums, _ := rec.uids.Nums()
			p.validity = rec.validity
			p.cursor.Store(uint32(nums[len(nums)-1]))
			a := &mailAccountState{ctx: ctx, link: l, config: accounts[0], control: p, writer: p, fast: make(chan mailWork, 16)}
			a.maintenance.epoch = l.attached
			completed := make(chan error, 1)
			inflight := 0
			var wait sync.WaitGroup
			a.processMaintenance(&p, completed, &inflight, &wait)
			a.mu.Lock()
			conservative := a.collectionPaged
			a.mu.Unlock()
			if !conservative {
				t.Fatal("control mutation failure did not select a message-bounded retry")
			}
			c.mu.Lock()
			for _, failure := range c.quarantines {
				failure.retryAt = time.Time{}
			}
			c.mu.Unlock()
			for n := 0; n < 4; n++ {
				a.processMaintenance(&p, completed, &inflight, &wait)
			}
			if p == nil {
				t.Fatal("bounded control retry lost its writer")
			}
			status, e := p.client.Status(folder, &imap.StatusOptions{NumMessages: true}).Wait()
			if e != nil || status.NumMessages == nil || *status.NumMessages != 0 {
				t.Fatalf("bounded control retries did not empty the consumed prefix: %v", e)
			}
			if c.snapshot().Paused {
				t.Fatal("successful audit did not clear quarantine")
			}
		})
	}
}
