package mailstream

import (
	"context"
	"testing"
)

// Uses the same account-owner interface as frozen RC12, so the old global
// failure can be reproduced without replacing production transport logic.
func TestIndependentPhysicalAccountCollectsAfterPeerCleanupTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := &IMAPLink{identity: "field-quarantine"}
	a := &mailAccountState{ctx: ctx, link: l, config: MailAccount{ID: "a01", Host: "fixture", Username: "one"}, spent: 48 << 20}
	b := &mailAccountState{ctx: ctx, link: l, config: MailAccount{ID: "a02", Host: "fixture", Username: "two"}}
	c := NewReclamationCoordinator()
	c.folder("owned-slot")
	c.stage("expunge")
	c.pause("CLEANUP_TIMEOUT", a)
	if !c.acquire(b) {
		t.Fatal("one failed account globally blocked independent physical mailbox collection")
	}
	c.release()
	if a.spent != 48<<20 || !c.snapshot().Paused {
		t.Fatal("unknown deletion outcome was refunded or forgotten")
	}
}
