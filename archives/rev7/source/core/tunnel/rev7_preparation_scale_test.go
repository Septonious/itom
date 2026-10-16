package tunnel

import (
	"context"
	"fmt"
	"itom.local/core/wire"
	"testing"
	"time"
)

// A fifteen-account profile has thirteen bulk services after its two quiet
// reserves. The old eight-worker preparation ceiling hid five healthy paths.
func TestRev7FifteenAccountsCanPrepareAllThirteenBulkServices(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	var owner *lane
	for i := 1; i <= 15; i++ {
		l := stripeTestLane(g, fmt.Sprintf("a%02d", i), false)
		defer l.cancel()
		l.stripe = g
		g.lanes[l.account.ID] = l
		if owner == nil {
			owner = l
			owner.admitted.Store(1)
			g.owners[1] = owner
		}
	}
	var releases []func()
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	for i := 0; i < 13; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		release, err := owner.AcquireBulkPreparation(ctx)
		cancel()
		if err != nil {
			t.Fatalf("healthy bulk service %d/13 was hidden by scheduler ceiling: %v", i+1, err)
		}
		releases = append(releases, release)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if release, err := owner.AcquireBulkPreparation(ctx); err == nil {
		release()
		t.Fatal("preparation borrowed a quiet reserve or exceeded actual physical capacity")
	}
}
