package tunnel

import (
	"context"
	"fmt"
	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
	"testing"
	"time"
)

func checkCleanupServiceKeepsCarrierAndFIFOProgress(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	a := stripeTestLane(g, "a01", false)
	b := stripeTestLane(g, "a02", false)
	c := stripeTestLane(g, "a03", false)
	d := stripeTestLane(g, "a04", false)
	for _, l := range []*lane{a, b, c, d} {
		defer l.cancel()
		if err := g.attach(l); err != nil {
			t.Fatal(err)
		}
	}
	if !g.beginCleanup(a) || !g.beginCleanup(b) || !g.beginCleanup(d) || g.beginCleanup(c) {
		t.Fatal("cleanup did not keep one healthy carrier available")
	}
	if c.cleanupWanted.Load() {
		t.Fatal("waiting cleaner excluded available fast carrier")
	}
	g.endCleanup(a)
	if g.beginCleanup(a) {
		t.Fatal("repeated pressure overtook queued cleaner")
	}
	if !g.beginCleanup(c) {
		t.Fatal("FIFO cleaner did not receive released service")
	}
	g.endCleanup(b)
	if !g.beginCleanup(a) {
		t.Fatal("former cleaner did not recover after FIFO turn")
	}
	g.endCleanup(a)
	g.endCleanup(d)
	g.endCleanup(c)
	if len(g.cleanupActive) != 0 || len(g.cleanupQueue) != 0 {
		t.Fatal("cleanup scheduler retained state")
	}
}
func checkCleanupServiceDetachesCanceledWaiter(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	a := stripeTestLane(g, "a01", false)
	b := stripeTestLane(g, "a02", false)
	defer a.cancel()
	defer b.cancel()
	g.attach(a)
	g.attach(b)
	if !g.beginCleanup(a) || g.beginCleanup(b) {
		t.Fatal("two-carrier reservation failed")
	}
	b.cancel()
	g.detach(b)
	g.endCleanup(a)
	if !g.beginCleanup(a) {
		t.Fatal("single remaining carrier cannot reclaim finite debt")
	}
	g.endCleanup(a)
}

func TestQueuedCleanupIsNotAbandonedWhenOwnerPressureChanges(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	a := stripeTestLane(g, "a01", false)
	b := stripeTestLane(g, "a02", false)
	defer a.cancel()
	defer b.cancel()
	g.attach(a)
	g.attach(b)
	g.owners[1] = a
	g.owners[3] = b
	a.remoteGC = map[journal.AttemptRef]remoteCleanup{{ID: 1}: {}}
	if !g.beginCleanup(b) {
		t.Fatal("fixture reservation failed")
	}
	if a.startCleanup(cleanupRemote) || !a.cleanupQueued {
		t.Fatal("cleanup was not retained for actor retry")
	}
	delete(g.owners, 3)
	clear(a.remoteGC)
	g.endCleanup(b)
	if a.startCleanup(a.cleanupQueuedKind) || a.cleanupQueued {
		t.Fatal("empty queued cleanup did not release its turn")
	}
	if !g.beginCleanup(b) {
		t.Fatal("abandoned owner queue blocked sibling cleanup")
	}
	g.endCleanup(b)
}

func TestMandatoryRemoteCleanupTurnSurvivesOwnerDeferral(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	a := stripeTestLane(g, "a01", false)
	b := stripeTestLane(g, "a02", false)
	defer a.cancel()
	defer b.cancel()
	g.attach(a)
	g.attach(b)
	g.owners[1] = a
	u := servercfg.User{ID: "12345678", Up: "itom-u-up", Down: "itom-u-down"}
	a.account.Mail.Host = "mail.example"
	store, err := ledger(t.TempDir(), "data", a.account, u, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	a.user = u
	a.data = store
	a.mail = &memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}
	a.gcDone = make(chan cleanupResult)
	a.remoteGC = map[journal.AttemptRef]remoteCleanup{{ID: 1}: {message: mailbox.Message{Folder: u.Up, Validity: 1, UID: 1}}}
	a.gcPreferRemote = true
	if !a.startCleanup(cleanupRemote) {
		t.Fatal("idle sibling deferred mandatory proof and suppressed local progress")
	}
	// Keep completion unconsumed: physical service must release its guard even
	// while the actor is busy. A deferred release after publishing deadlocks here.
	deadline := time.Now().Add(time.Second)
	for a.maintenanceIO.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if a.maintenanceIO.Load() != 0 {
		a.cancel()
		a.workers.Wait()
		t.Fatal("physical cleanup guard waited for actor completion")
	}
	result := <-a.gcDone
	a.workers.Wait()
	if len(g.cleanupActive) != 0 {
		t.Fatal("finished physical service retained reservation")
	}
	if result.err == nil {
		t.Fatal("fixture's unproved remote row was treated as deleted")
	}
	if err = store.Audit(); err != nil {
		t.Fatal(err)
	}
}

type blockedPresenceMail struct {
	mailbox.Transport
	entered, release chan struct{}
}

func (m *blockedPresenceMail) Present(ctx context.Context, _ string, _ uint32, _ []uint32) (map[uint32]bool, error) {
	close(m.entered)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-m.release:
		return map[uint32]bool{}, nil
	}
}

func TestReadOnlyPresenceProofDoesNotReserveOrBlockFastIO(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	l := stripeTestLane(g, "a01", false)
	defer l.cancel()
	if err := g.attach(l); err != nil {
		t.Fatal(err)
	}
	m := &blockedPresenceMail{entered: make(chan struct{}), release: make(chan struct{})}
	l.mail = m
	l.recvFolder = "itom-u-up"
	l.gcDone = make(chan cleanupResult, 1)
	l.known = map[uint64]mailbox.Message{1: {Validity: 1, UID: 1}}
	if !l.startCleanup(cleanupPresence) {
		t.Fatal("presence proof did not start")
	}
	select {
	case <-m.entered:
	case <-time.After(time.Second):
		l.cancel()
		l.workers.Wait()
		t.Fatal("presence command did not start")
	}
	if !l.gcBusy || l.gcActive.Load() || l.cleanupWanted.Load() || len(g.cleanupActive) != 0 || !l.tryBeginFastPhysical() {
		close(m.release)
		l.workers.Wait()
		t.Fatal("read-only presence consumed DATA maintenance service")
	}
	l.endFastPhysical()
	close(m.release)
	l.workers.Wait()
	r := <-l.gcDone
	if r.err != nil || len(r.local) != 0 || len(r.remote) != 0 || l.maintenanceIO.Load() != 0 {
		t.Fatal("presence produced a physical refund or retained fast guard", r)
	}
}

func checkCleanupCapacityTracksFiveAndTenAvailableAccounts(t *testing.T) {
	for _, n := range []int{5, 10} {
		checkScenarioBoundary(t, fmt.Sprint(n), func(t *testing.T) {
			g := newStripeGroup(wire.ID{1}, 1)
			var lanes []*lane
			for i := 0; i < n; i++ {
				l := stripeTestLane(g, fmt.Sprintf("a%02d", i+1), false)
				defer l.cancel()
				g.attach(l)
				lanes = append(lanes, l)
			}
			spares := 2
			order := append([]*lane{lanes[0]}, lanes[2:]...)
			order = append(order, lanes[1])
			for i, l := range order {
				admitted := g.beginCleanup(l)
				if admitted != (i < n-spares) {
					t.Fatal("cleanup capacity did not scale", n, i, admitted)
				}
				l.gcActive.Store(admitted)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			selected, err := g.selectForClass(ctx, nil, true)
			if err != nil || selected.gcActive.Load() {
				t.Fatal("no healthy fast carrier remained", err)
			}
			for _, l := range lanes {
				g.endCleanup(l)
			}
		})
	}
}

func checkCleanupKeepsGrantReadyFastSpareDuringLeaseRollover(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	var lanes []*lane
	for i := 0; i < 5; i++ {
		l := stripeTestLane(g, fmt.Sprintf("a%02d", i+1), false)
		defer l.cancel()
		g.attach(l)
		lanes = append(lanes, l)
	}
	for _, l := range lanes[1:] {
		<-l.fastGrants
	}
	if g.beginCleanup(lanes[0]) {
		t.Fatal("READY without permits displaced the only usable fast spare")
	}
	if !g.beginCleanup(lanes[1]) {
		t.Fatal("permit-starved carrier could not reclaim/refill ahead of protected spare")
	}
	g.endCleanup(lanes[1])
	lanes[1].fastGrants <- wire.QuotaKey{Grant: 2}
	if g.beginCleanup(lanes[0]) {
		t.Fatal("cleanup consumed one of the two usable spares")
	}
	lanes[2].fastGrants <- wire.QuotaKey{Grant: 3}
	if !g.beginCleanup(lanes[0]) {
		t.Fatal("FIFO did not resume after a third usable spare returned")
	}
	g.endCleanup(lanes[0])
}

type auditedMemoryMail struct {
	*memoryMail
	entered, release chan struct{}
}

func (m *auditedMemoryMail) RenewReaderSelection(ctx context.Context) error {
	close(m.entered)
	select {
	case <-m.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func TestCleanupNeverStartsHiddenReceiveAudit(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	l := stripeTestLane(g, "a01", true)
	defer l.cancel()
	g.attach(l)
	l.user = servercfg.User{ID: "12345678", Up: "itom-u-up", Down: "itom-u-down"}
	l.account.Mail.Host = "mail.example"
	store, err := ledger(t.TempDir(), "data", l.account, l.user, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	l.data = store
	l.cipher = cleanupFixtureEpoch(t)
	l.sendFolder = l.user.Down
	l.gcDone = make(chan cleanupResult, 1)
	m := &auditedMemoryMail{memoryMail: &memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}, entered: make(chan struct{}), release: make(chan struct{})}
	m.Ensure(l.ctx, l.sendFolder)
	l.mail = m
	if !l.startCleanup(cleanupLocalData) {
		t.Fatal("cleanup did not start")
	}
	l.workers.Wait()
	select {
	case <-m.entered:
		t.Fatal("cleanup started hidden receive SELECT")
	default:
	}
	g.mu.Lock()
	reserved := g.cleanupActive[l]
	g.mu.Unlock()
	available := l.tryBeginFastPhysical()
	if available {
		l.endFastPhysical()
	}
	if reserved || l.gcActive.Load() || l.maintenanceIO.Load() != 0 || !available {
		close(m.release)
		l.workers.Wait()
		t.Fatal("read-only audit retained physical DATA admission")
	}
	close(m.release)
	l.workers.Wait()
	result := <-l.gcDone
	if result.err != nil || len(result.local) != 0 || len(result.remote) != 0 {
		t.Fatal("audit produced a refund", result)
	}
}

func checkCleanupServiceHandsReleasedCapacityToOldestWaiter(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	a, b, c := stripeTestLane(g, "a01", true), stripeTestLane(g, "a02", true), stripeTestLane(g, "a03", true)
	for _, l := range []*lane{a, b, c} {
		defer l.cancel()
		l.cleanupWake = make(chan struct{}, 1)
		if err := g.attach(l); err != nil {
			t.Fatal(err)
		}
	}
	if !g.beginCleanup(a) || !g.beginCleanup(b) || g.beginCleanup(c) {
		t.Fatal("fixture admission")
	}
	g.endCleanup(a)
	select {
	case <-c.cleanupWake:
	default:
		t.Fatal("released capacity waits for actor timer")
	}
	select {
	case <-a.cleanupWake:
		t.Fatal("release woke nonwaiting actor")
	default:
	}
	if g.beginCleanup(a) {
		t.Fatal("returning cleaner overtook FIFO")
	}
	if !g.beginCleanup(c) {
		t.Fatal("notified waiter was not admitted")
	}
	// Duplicate notifications are coalesced; stale ones never bypass admission.
	g.endCleanup(b)
	select {
	case <-a.cleanupWake:
	default:
		t.Fatal("second handoff lost")
	}
	if !g.beginCleanup(a) || g.beginCleanup(b) {
		t.Fatal("handoff consumed fast spare")
	}
}

func checkCleanupServiceHandoffSkipsCanceledQueueHead(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	ls := []*lane{}
	for i := 0; i < 5; i++ {
		ids := []string{"a01", "a03", "a04", "a05", "a02"}
		l := stripeTestLane(g, ids[i], true)
		defer l.cancel()
		l.cleanupWake = make(chan struct{}, 1)
		if err := g.attach(l); err != nil {
			t.Fatal(err)
		}
		ls = append(ls, l)
	}
	if !g.beginCleanup(ls[0]) || !g.beginCleanup(ls[1]) || !g.beginCleanup(ls[2]) || g.beginCleanup(ls[3]) || g.beginCleanup(ls[4]) {
		t.Fatal("fixture admission")
	}
	ls[3].cancel()
	g.endCleanup(ls[0])
	select {
	case <-ls[4].cleanupWake:
	default:
		t.Fatal("canceled queue head blocked handoff")
	}
	if !g.beginCleanup(ls[4]) {
		t.Fatal("live waiter blocked after canceled head")
	}
}

// Resource-independent checks form one scenario; lifecycle and concurrency
// checks in this file remain independently scheduled.
func TestScenarioCleanupService(t *testing.T) {
	t.Log("check=checkCleanupServiceKeepsCarrierAndFIFOProgress")
	checkCleanupServiceKeepsCarrierAndFIFOProgress(t)
	t.Log("check=checkCleanupServiceDetachesCanceledWaiter")
	checkCleanupServiceDetachesCanceledWaiter(t)
	t.Log("check=checkCleanupCapacityTracksFiveAndTenAvailableAccounts")
	checkCleanupCapacityTracksFiveAndTenAvailableAccounts(t)
	t.Log("check=checkCleanupKeepsGrantReadyFastSpareDuringLeaseRollover")
	checkCleanupKeepsGrantReadyFastSpareDuringLeaseRollover(t)
	t.Log("check=checkCleanupServiceHandsReleasedCapacityToOldestWaiter")
	checkCleanupServiceHandsReleasedCapacityToOldestWaiter(t)
	t.Log("check=checkCleanupServiceHandoffSkipsCanceledQueueHead")
	checkCleanupServiceHandoffSkipsCanceledQueueHead(t)
}
