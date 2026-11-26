package mailstream

import (
	"context"
	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"testing"
	"time"
)

// The real provider repeatedly times out at EXPUNGE even after the initial
// bounded fallback. Model a provider that rejects batches above four UIDs.
type rc4SmallMutationSession struct{ *byteCollectionSession }

func (s *rc4SmallMutationSession) Expunge(w *imapserver.ExpungeWriter, u *imap.UIDSet) error {
	s.fixture.mu.Lock()
	n := len(s.fixture.deleted[s.user+"/"+s.selected])
	s.fixture.mu.Unlock()
	if n > 4 {
		return &imap.Error{Type: imap.StatusResponseTypeNo, Text: "fixture small mutation limit"}
	}
	return s.byteCollectionSession.Expunge(w, u)
}
func TestRC4CollectionCeilingLearnsFromRepeatedFailures(t *testing.T) {
	a := &mailAccountState{spent: 64 << 20, reserved: 64 << 20, recovered: 64 << 20}
	for _, want := range []uint64{32, 16, 8, 4, 2, 1, 1} {
		a.noteCollectionFailureLocked()
		if got := a.collectionMessageLimitLocked(); got != want {
			t.Fatalf("limit %d want %d", got, want)
		}
	}
	if a.spent != 64<<20 || a.reserved != 64<<20 || a.recovered != 64<<20 {
		t.Fatal("failure manufactured storage credit")
	}
}
func TestRC4SmallProviderMutationPagesKeepAuditFence(t *testing.T) {
	f := &byteCollectionFixture{sizes: map[string]map[imap.UID]int64{}, deleted: map[string]map[imap.UID]bool{}}
	accounts, _ := applicationMailServerWrapped(t, true, false, func(s imapserver.Session) imapserver.Session {
		return &rc4SmallMutationSession{&byteCollectionSession{Session: s, fixture: f}}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	p, e := openMailPipe(ctx, accounts[0])
	if e != nil {
		t.Fatal(e)
	}
	defer p.close()
	rec := seedCollectionFolder(t, p, "itom3-rc4-small", 12)
	nums, _ := rec.uids.Nums()
	target := uidWatermark{rec.validity, uint32(nums[len(nums)-1])}
	c := NewReclamationCoordinator()
	a := &mailAccountState{spent: rec.bytes, reserved: rec.bytes, recovered: rec.bytes}
	for i := 0; i < 4; i++ {
		a.noteCollectionFailureLocked()
	}
	for i := 0; i < 3; i++ {
		proof, n, done, e := p.collectPrefix(c, rec.folder, target, false, a.collectionMessageLimitLocked())
		if e != nil || n != 4 || done != (i == 2) || proof.UID > target.UID {
			t.Fatalf("turn %d proof=%+v n=%d done=%v err=%v", i, proof, n, done, e)
		}
		if a.spent != rec.bytes {
			t.Fatal("partial audit refunded historical debt")
		}
	}
}
