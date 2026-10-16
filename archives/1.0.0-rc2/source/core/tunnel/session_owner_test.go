package tunnel

import (
	"context"
	"fmt"
	"testing"

	"itom.local/core/servercfg"
	"itom.local/core/wire"
)

func TestAccountScaleSharesOneLogicalSessionBudget(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g := newStripeGroup(wire.ID{7}, 1)
	first := stripeTestLane(g, "a01", false)
	defer first.cancel()
	first.ownerCtx = ctx
	first.actorReady = make(chan struct{})
	close(first.actorReady)
	owner, err := g.claimOwner(first)
	if err != nil || owner != first {
		t.Fatal(err)
	}
	for i := 2; i <= 128; i++ {
		l := stripeTestLane(g, fmt.Sprintf("a%02d", i), false)
		l.ownerCtx = ctx
		if err = g.attach(l); err != nil {
			t.Fatal(err)
		}
		got, err := g.claimOwner(l)
		if err != nil || got != first || g.accountOwner(l.account.ID) != first {
			t.Fatal("account created another logical budget", i, err)
		}
		l.cancel()
	}
	if len(g.logicalOwners) != 1 {
		t.Fatal("flow memory multiplied by account count")
	}
	first.cancel()
	if g.accountOwner("a128") != first {
		t.Fatal("physical owner loss destroyed logical session")
	}
	g.releaseOwner(first)
}

func TestSharedOwnerCarrierSnapshotDoesNotMultiplyStreamCount(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	owner := &lane{account: servercfg.Account{ID: "a01"}}
	owner.active.Store(3)
	g.sessionOwner = owner
	g.owners[1] = owner
	g.owners[3] = owner
	g.owners[5] = owner
	g.streamAccount[1] = "a01"
	g.streamAccount[3] = "a02"
	g.streamAccount[5] = "a03"
	for _, id := range []string{"a01", "a02", "a03"} {
		if n := g.carrierStreamCount(id); n != 1 {
			t.Fatal("shared owner multiplied account streams", id, n)
		}
	}
}
