package transport_test

import (
	"context"
	"testing"
	"time"

	"itom.local/core/transport"
)

func TestQuietInboxDoesNotExitIdleForUnrelatedOutgoingWork(t *testing.T) {
	f := serverPublications(t, transport.Bulk, 4<<20)
	box := &sharedMailbox{}
	x := box.selectedWithIdleService(t, "bulk", nil, true, false)
	defer x.Close()
	inbox, err := transport.NewClientInbox(x, transport.DefaultInboxLimits(), f.incoming)
	if err != nil {
		t.Fatal(err)
	}
	limits := transport.DefaultInboxServiceLimits()
	limits.PreferIdle = true
	limits.IdleAudit = time.Second
	worker, err := transport.NewInboxWorker(inbox, limits)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	until := time.NewTimer(3 * time.Second)
	defer until.Stop()
	before := x.Snapshot().Idle
	for {
		if before.Started > before.Completed {
			select {
			case <-until.C:
				t.Fatal("quiet reader did not settle after initial EXISTS")
			case <-time.After(10 * time.Millisecond):
			}
			after := x.Snapshot().Idle
			if after.Started == before.Started && after.Completed == before.Completed {
				break
			}
		}
		select {
		case <-until.C:
			t.Fatal("quiet selected reader never entered IDLE")
		case <-time.After(time.Millisecond):
		}
		before = x.Snapshot().Idle
	}
	// This session has no publisher. Its local queue neither creates a peer
	// mailbox message nor frees a deferred incoming delivery.
	for range 20 {
		if err := f.client.Queue(1, []byte("unrelated outgoing bytes"), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case err := <-done:
		t.Fatal("quiet reader stopped", err)
	case <-time.After(25 * time.Millisecond):
	}
	if snapshot := x.Snapshot(); snapshot.Idle.Completed != before.Completed {
		t.Fatal("outgoing demand needlessly interrupted quiet receiver", snapshot)
	}
}
