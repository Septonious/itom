package tunnel

import (
	"itom.local/mailstream"
	"testing"
	"time"
)

func TestRC21CellularDemandDoesNotDependOnCollapsedRate(t *testing.T) {
	for _, tc := range []struct {
		s    mailstream.Snapshot
		r    uint64
		want bool
	}{
		{mailstream.Snapshot{Streams: 8, BulkOutstanding: 194191}, 0, true},
		{mailstream.Snapshot{Streams: 8, PeerDemandFresh: true, PeerBulkPending: 194191}, 0, true},
		{mailstream.Snapshot{Streams: 8, PeerDemandFresh: false, PeerBulkPending: 194191}, 0, false},
		{mailstream.Snapshot{Streams: 8}, 0, false},
		{mailstream.Snapshot{Streams: 8}, 9000, true},
	} {
		if got := cellularTrafficDemand(tc.s, tc.r); got != tc.want {
			t.Fatalf("%+v: %v", tc, got)
		}
	}
}
func TestRC21RecoverOnePathBeforeFiveSecondCooldown(t *testing.T) {
	now := time.Unix(100, 0)
	p := cellularPolicy{limit: 1, base: 35, lastChange: now}
	p.decideRecovery(8, 35, true, 0, now.Add(time.Second))
	if got := p.decideRecovery(8, 35, true, 0, now.Add(2*time.Second)); got != 2 {
		t.Fatal(got)
	}
	p.decideRecovery(8, 45, true, 0, now.Add(3*time.Second))
	if got := p.decideRecovery(8, 45, true, 0, now.Add(4*time.Second)); got != 3 {
		t.Fatal(got)
	}
}

func TestRC21HealthyIdleNeverParksToOne(t *testing.T) {
	at := time.Unix(100, 0)
	var p cellularPolicy
	for i := 0; i < 90; i++ {
		p.decideRecovery(8, 40, false, 0, at.Add(time.Duration(i)*time.Second))
	}
	if p.limit != 2 {
		t.Fatal(p.limit)
	}
	for i := 90; i < 120; i++ {
		p.decideRecovery(8, 1100, false, 0, at.Add(time.Duration(i)*time.Second))
	}
	if p.limit != 1 {
		t.Fatal(p.limit)
	}
}
