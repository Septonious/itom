package mailstream

import (
	"context"
	"testing"
)

func TestRecoveryPreferenceDoesNotReserveAnIdleMutationLease(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := &IMAPLink{}
	preferred := &mailAccountState{ctx: ctx, link: l, config: MailAccount{Host: "fixture", Username: "preferred"}}
	ready := &mailAccountState{ctx: ctx, link: l, config: MailAccount{Host: "fixture", Username: "ready"}}
	c := NewReclamationCoordinator()
	c.preferRecovery(preferred, true)
	if !c.acquire(ready) {
		t.Fatal("idle mutation lease was reserved for a recovery owner with no request")
	}
	if c.acquire(preferred) {
		t.Fatal("parallel mutation admitted while another account was active")
	}
	c.release()
	if !c.acquire(preferred) {
		t.Fatal("ready recovery owner lost its preference")
	}
	c.release()
}
