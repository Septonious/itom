package tunnel

import (
	"context"
	"fmt"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/wire"
)

// Separate APPEND sockets still share a receiver UID/BODY queue. Two
// healthy accounts must remain free of new bulk BODYs while a normal ten-account
// pool is saturated, so HTTPS control and short DATA have a readable path.
func TestFieldBulkLeavesTwoReadableAccountsForInteractiveData(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	for i := 1; i <= 10; i++ {
		l := stripeTestLane(g, fmt.Sprintf("a%02d", i), false)
		defer l.cancel()
		if err := g.attach(l); err != nil {
			t.Fatal(err)
		}
	}
	g.mu.Lock()
	keys := []string{"a03", "a04", "a01", "a02"}
	g.prioritizeFastTargetsLocked(keys, nil)
	if (keys[0] != "a01" && keys[0] != "a02") || (keys[1] != "a01" && keys[1] != "a02") {
		t.Fatal("short DATA bypassed quiet BODY readers", keys)
	}
	g.mu.Unlock()
	var leases []stripeLease
	defer func() {
		for _, x := range leases {
			x.release()
		}
	}()
	for i := 0; i < 8; i++ {
		x, err := g.acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		leases = append(leases, x)
		if x.lane.account.ID == "a01" || x.lane.account.ID == "a02" {
			t.Fatal("bulk occupied the interactive receiver reserve")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if x, err := g.acquire(ctx); err == nil {
		x.release()
		t.Fatal("bulk consumed the last readable account")
	}
	fast, err := g.acquireForClass(context.Background(), nil, true)
	if err != nil || fast.lane.account.ID != "a01" {
		t.Fatal("short DATA lost reserved receiver", err)
	}
	fast.release()
}

func TestFieldLargeBodiesTriggerOwnerCleanupBeforeGrantExhaustion(t *testing.T) {
	g := newStripeGroup(wire.ID{2}, 1)
	owner, sibling := stripeTestLane(g, "a01", true), stripeTestLane(g, "a02", true)
	defer owner.cancel()
	defer sibling.cancel()
	if err := g.attach(owner); err != nil {
		t.Fatal(err)
	}
	if err := g.attach(sibling); err != nil {
		t.Fatal(err)
	}
	if _, err := g.allocate(owner); err != nil {
		t.Fatal(err)
	}
	owner.remoteGC = map[journal.AttemptRef]remoteCleanup{}
	for i := 1; i <= 3; i++ {
		owner.remoteGC[journal.AttemptRef{ID: uint64(i)}] = remoteCleanup{message: mailbox.Message{MIME: make([]byte, 384<<10)}}
	}
	if !owner.ownerCleanupPressure(cleanupRemote) || owner.deferOwnerCleanup(cleanupRemote) {
		t.Fatal("three large BODYs deferred until16 messages; byte lease can exhaust before owner cleanup")
	}
}

func TestFieldCleanupDoesNotCountBulkOnlyAccountsAsFastSpares(t *testing.T) {
	g := newStripeGroup(wire.ID{3}, 1)
	var lanes []*lane
	for i := 1; i <= 5; i++ {
		l := stripeTestLane(g, fmt.Sprintf("a%02d", i), false)
		defer l.cancel()
		if i > 2 {
			<-l.fastGrants
			l.grants <- wire.QuotaKey{Grant: uint64(i)}
		}
		if err := g.attach(l); err != nil {
			t.Fatal(err)
		}
		lanes = append(lanes, l)
	}
	if g.beginCleanup(lanes[0]) {
		g.endCleanup(lanes[0])
		t.Fatal("bulk-only grants were counted as usable interactive spares")
	}
	if !g.beginCleanup(lanes[2]) {
		t.Fatal("debt-bound account could not recover while true fast spares were preserved")
	}
	g.endCleanup(lanes[2])
}

func TestFieldQuietReaderCleanupIsSerialized(t *testing.T) {
	g := newStripeGroup(wire.ID{4}, 1)
	var ls []*lane
	for i := 1; i <= 5; i++ {
		l := stripeTestLane(g, fmt.Sprintf("a%02d", i), false)
		defer l.cancel()
		g.attach(l)
		ls = append(ls, l)
	}
	if !g.beginCleanup(ls[0]) || g.beginCleanup(ls[1]) {
		t.Fatal("both quiet readers entered cleanup")
	}
	if !g.beginCleanup(ls[2]) {
		t.Fatal("quiet reserve blocked bulk cleanup")
	}
	g.endCleanup(ls[0])
	if !g.beginCleanup(ls[1]) {
		t.Fatal("quiet cleanup failed FIFO handoff")
	}
	g.endCleanup(ls[1])
	g.endCleanup(ls[2])
}

type isolatedRoleTestMail struct{ mailbox.Transport }

func (*isolatedRoleTestMail) DataMaintenanceIsolated() bool { return true }
func (*isolatedRoleTestMail) DataAvailable() bool           { return true }
func (*isolatedRoleTestMail) DataParallelism() int          { return 1 }
func TestQuietReaderReserveSurvivesIndependentControlAppend(t *testing.T) {
	g := newStripeGroup(wire.ID{5}, 1)
	for i := 1; i <= 5; i++ {
		l := stripeTestLane(g, fmt.Sprintf("a%02d", i), false)
		defer l.cancel()
		l.mail = &isolatedRoleTestMail{}
		if i <= 2 {
			l.controlWriting.Store(1)
		}
		if err := g.attach(l); err != nil {
			t.Fatal(err)
		}
	}
	g.mu.Lock()
	keys := []string{"a03", "a04", "a05", "a01", "a02"}
	g.prioritizeFastTargetsLocked(keys, nil)
	g.mu.Unlock()
	if keys[0] != "a01" && keys[0] != "a02" {
		t.Fatal("independent control APPEND routed fast DATA into bulk BODY queue", keys)
	}
}
