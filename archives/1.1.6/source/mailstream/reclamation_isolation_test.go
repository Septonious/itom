package mailstream

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestFailedCollectionDoesNotQuarantineIndependentPhysicalAccounts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := &IMAPLink{identity: "same-namespace"}
	a := &mailAccountState{ctx: ctx, link: l, config: MailAccount{ID: "a01", Host: "imap.fixture", Username: "one"}, spent: 42 << 20}
	b := &mailAccountState{ctx: ctx, link: l, config: MailAccount{ID: "a02", Host: "imap.fixture", Username: "two"}}
	c := NewReclamationCoordinator()
	c.folder("owned-slot")
	c.stage("expunge")
	c.pause("CLEANUP_TIMEOUT", a)
	if !c.acquire(b) {
		t.Fatal("one provider account blocked independent account collection")
	}
	if c.acquire(a) {
		t.Fatal("two mutations acquired the shared lease")
	}
	c.completed(b, "owned-slot")
	c.release()
	if !c.snapshot().Paused || a.spent != 42<<20 {
		t.Fatal("other account completion cleared unknown debt")
	}
	alias := &mailAccountState{ctx: ctx, link: l, config: a.config}
	alias.config.ID = "renamed-alias"
	if c.permitted(alias, "owned-slot") {
		t.Fatal("routing alias escaped physical account quarantine")
	}
	c.mu.Lock()
	c.failureLocked(a).retryAt = time.Now().Add(-time.Second)
	c.mu.Unlock()
	if c.permitted(a, "another-slot") || !c.acquire(a) {
		t.Fatal("retry failed to stay fenced to the affected folder")
	}
	c.completed(a, "another-slot")
	if !c.snapshot().Paused {
		t.Fatal("wrong-folder audit cleared quarantine")
	}
	c.completed(a, "owned-slot")
	c.release()
	if c.snapshot().Paused || a.spent != 42<<20 {
		t.Fatal("coordinator changed debt instead of requiring audited storage owner")
	}
}

func TestControlMaintenanceAppendDrainDoesNotHoldGlobalMutationLease(t *testing.T) {
	accounts, _ := applicationMailServer(t, true, false)
	ctx, cancel := context.WithCancel(context.Background())
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
	a.maintenance.requested = true
	done := make(chan struct{})
	go func() {
		defer close(done)
		inFlight := 1
		var joined sync.WaitGroup
		a.processMaintenance(&p, make(chan error), &inFlight, &joined)
	}()
	waitRetention(t, ctx, func() bool { return a.reclaiming.Load() }, "control maintenance did not begin APPEND drain")
	if !c.acquire(b) {
		cancel()
		<-done
		t.Fatal("control maintenance held the global lease while waiting for APPEND tags")
	}
	c.release()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled maintenance failed to join")
	}
	if c.snapshot().Paused {
		t.Fatal("unmutated APPEND cancellation quarantined an account")
	}
}

func TestInactiveNamespaceCanAuditFailedFolderWhileAnotherSessionRuns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := &IMAPLink{identity: "old-namespace"}
	a := &mailAccountState{ctx: ctx, link: l, config: MailAccount{ID: "a01", Host: "imap.fixture", Username: "same-physical-account"}}
	c := NewReclamationCoordinator()
	c.sessions = map[*IMAPLink]bool{&IMAPLink{identity: "active-other-namespace"}: true}
	c.folder("owned-old-session-slot")
	c.stage("audit_select")
	c.pause("CLEANUP_TIMEOUT", a)
	if c.idleLease(a, func() {}) {
		t.Fatal("failed-folder retry ignored backoff")
	}
	c.mu.Lock()
	c.failureLocked(a).retryAt = time.Now().Add(-time.Second)
	c.mu.Unlock()
	if !c.idleLease(a, func() {}) {
		t.Fatal("active other namespace stranded the owning account's fenced audit")
	}
	c.endIdle()
	c.completed(a, "owned-old-session-slot")
	if c.idleLease(a, func() {}) {
		t.Fatal("ordinary historical sweep ran during an active session")
	}
}
