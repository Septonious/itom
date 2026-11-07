package mailstream

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Several real TLS writers have terminal APPEND replies outstanding when
// cleanup becomes eligible after a speedtest. Limit preparation as well as
// mutation, without letting a slow drain own the shared mutation lease.
func TestConcurrentCleanupDrainsLeaveOtherWritersAvailable(t *testing.T) {
	accounts, _ := applicationMailServer(t, true, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := NewReclamationCoordinator()
	l := &IMAPLink{ctx: ctx, reclaimer: c, changed: make(chan struct{})}
	var actors []*mailAccountState
	done := make(chan struct{}, 3)
	for i := range 3 {
		p, err := openMailPipe(ctx, accounts[i%len(accounts)])
		if err != nil {
			t.Fatal(err)
		}
		defer p.close()
		a := &mailAccountState{ctx: ctx, link: l, config: accounts[i%len(accounts)], writer: p}
		actors = append(actors, a)
		go func() {
			defer func() { done <- struct{}{} }()
			inFlight := 1
			var joined sync.WaitGroup
			a.processCleanup(cleanupJob{account: a, record: &segmentRecord{queued: true, folder: "itom3-drain"}}, &p, make(chan error), &inFlight, &joined)
		}()
	}
	// The third attempt must yield promptly rather than join the APPEND drain.
	completed := 0
	select {
	case <-done:
		completed++
	case <-time.After(300 * time.Millisecond):
		t.Error("optional cleanup excluded all three writers while awaiting APPEND replies")
	}
	borrowed := 0
	for _, a := range actors {
		if a.reclaiming.Load() {
			borrowed++
		}
	}
	if borrowed > 2 {
		t.Errorf("too many borrowed writers: %d", borrowed)
	}
	other := &mailAccountState{ctx: ctx, link: l, config: accounts[0]}
	if !c.acquire(other) {
		t.Error("APPEND drains owned the mutation lease")
	} else {
		c.release()
	}
	cancel()
	deadline := time.After(time.Second)
	for completed < len(actors) {
		select {
		case <-done:
			completed++
		case <-deadline:
			t.Fatal("cancelled cleanup drain did not join")
		}
	}
	for _, a := range actors {
		if a.reclaiming.Load() {
			t.Fatal("borrowed writer leaked after cancellation")
		}
	}
	if c.snapshot().Paused {
		t.Fatal("cancelled preparation quarantined account")
	}
}
