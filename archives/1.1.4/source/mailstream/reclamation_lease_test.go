package mailstream

import (
	"context"
	"sync"
	"testing"
	"time"
)

// One account can have slow terminal APPEND replies even when another is ready
// to collect an already-consumed slot. Waiting for those replies must not own
// the server-wide mutation lease or quarantine unrelated accounts on cancel.
func TestSlowAppendDrainDoesNotOwnSharedCleanupLease(t *testing.T) {
	accounts, _ := applicationMailServer(t, true, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p, err := openMailPipe(ctx, accounts[0])
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	c := NewReclamationCoordinator()
	l := &IMAPLink{ctx: ctx, reclaimer: c, changed: make(chan struct{})}
	a := &mailAccountState{ctx: ctx, link: l, config: accounts[0], writer: p}
	b := &mailAccountState{ctx: ctx, link: l, config: accounts[1]}
	rec := &segmentRecord{queued: true, folder: "itom3-fixture-unmutated"}
	done := make(chan struct{})
	go func() {
		defer close(done)
		inFlight := 1
		var joined sync.WaitGroup
		a.processCleanup(cleanupJob{account: a, record: rec}, &p, make(chan error), &inFlight, &joined)
	}()
	waitRetention(t, ctx, func() bool { return a.reclaiming.Load() }, "APPEND drain did not start")
	if !c.acquire(b) {
		cancel()
		<-done
		t.Fatal("slow APPEND replies held the shared mutation lease and blocked another account")
	}
	c.release()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled APPEND drain did not join")
	}
	if c.snapshot().Paused {
		t.Fatal("cancelled pre-mutation drain quarantined unrelated accounts")
	}
}
