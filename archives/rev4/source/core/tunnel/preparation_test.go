package tunnel

import (
	"context"
	"errors"
	"itom.local/core/wire"
	"testing"
	"time"
)

func TestPreparationSharesPhysicalCapacityAndCancellation(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	a, b := stripeTestLane(g, "a01", false), stripeTestLane(g, "a02", false)
	defer a.cancel()
	defer b.cancel()
	for i, l := range []*lane{a, b} {
		l.stripe = g
		l.admitted.Store(1)
		g.lanes[l.account.ID] = l
		g.owners[uint64(i*2+1)] = l
	}
	first, err := a.AcquireBulkPreparation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		release, err := a.AcquireBulkPreparation(ctx)
		if release != nil {
			release()
		}
		done <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		g.mu.Lock()
		n := len(g.preparation)
		g.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second owner request did not wait")
		}
		time.Sleep(time.Millisecond)
	}
	second, err := b.AcquireBulkPreparation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	if g.prepared != 2 || g.preparedByOwner[a] != 1 || g.preparedByOwner[b] != 1 {
		t.Error("one owner monopolized physical preparation")
	}
	g.mu.Unlock()
	cancel()
	if err = <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	first()
	first()
	second()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.prepared != 0 || len(g.preparation) != 0 {
		t.Fatal("preparation lease leaked")
	}
}
