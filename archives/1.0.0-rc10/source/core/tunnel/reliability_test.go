package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
)

func reliabilityLane(parent context.Context, account string, logical wire.ID, generation, epoch uint64) *lane {
	ctx, cancel := context.WithCancel(parent)
	l := &lane{
		ctx:            ctx,
		cancel:         cancel,
		account:        servercfg.Account{ID: account},
		logicalSession: logical,
		generation:     generation,
		carrierEpoch:   epoch,
		grants:         make(chan wire.QuotaKey, 8),
		fastGrants:     make(chan wire.QuotaKey, 8),
		remote:         map[wire.QuotaKey][]remoteLogicalAttempt{},
		obsolete:       map[journal.AttemptRef]bool{},
		mailReceipts:   map[uint64]bool{},
	}
	l.ready.Store(true)
	l.markRemoteProgress()
	return l
}

func checkStripeDetachDoesNotCascadeCancelOtherOwner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logical := wire.ID{1}
	g := newStripeGroup(logical, 1)
	owner := reliabilityLane(ctx, "a01", logical, 1, 1)
	physical := reliabilityLane(ctx, "a02", logical, 1, 1)
	defer owner.cancel()
	defer physical.cancel()
	if err := g.attach(owner); err != nil {
		t.Fatal(err)
	}
	if err := g.attach(physical); err != nil {
		t.Fatal(err)
	}
	stream, err := g.allocate(owner)
	if err != nil {
		t.Fatal(err)
	}
	key := wire.QuotaKey{Store: wire.ID{2}, Epoch: 1, Grant: 7, Session: wire.ID{3}, User: "u", Account: "a02"}
	ref := journal.AttemptRef{Grant: journal.GrantRef{ID: 9}, ID: 4}
	physical.registerRemoteLogical(key, owner, []uint64{101})
	physical.registerOutgoing(outgoing{ref: ref, owner: owner, attempts: []uint64{102}})

	g.detach(physical)
	select {
	case <-owner.ctx.Done():
		t.Fatal("losing a physical carrier canceled an unrelated logical owner")
	default:
	}
	if got, known := g.route(stream); !known || got != owner {
		t.Fatalf("owner route was damaged by unrelated physical detach: owner=%p known=%v", got, known)
	}
	remote := g.takeRemote("a02", key)
	if len(remote) != 1 || remote[0].owner != owner || remote[0].id != 101 {
		t.Fatalf("remote cross-carrier ownership was lost on detach: %+v", remote)
	}
	local := g.takeLocal("a02", ref)
	if len(local) != 1 || local[0].owner != owner || local[0].id != 102 {
		t.Fatalf("local cross-carrier ownership was lost on detach: %+v", local)
	}
}

func checkStripeRemoteGrantSelectionSkipsGrantStarvedCarrier(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	logical := wire.ID{1}
	g := newStripeGroup(logical, 1)
	starved := reliabilityLane(ctx, "a01", logical, 1, 1)
	ready := reliabilityLane(ctx, "a02", logical, 1, 1)
	defer starved.cancel()
	defer ready.cancel()
	if err := g.attach(starved); err != nil {
		t.Fatal(err)
	}
	if err := g.attach(ready); err != nil {
		t.Fatal(err)
	}
	key := wire.QuotaKey{Store: wire.ID{2}, Epoch: 1, Grant: 11, Session: wire.ID{3}, User: "u", Account: "a02"}
	ready.grants <- key

	lane, got, err := g.takeRemoteGrantForClass(ctx, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if lane != ready || got != key {
		t.Fatalf("grant-aware selection picked starved carrier: lane=%v key=%+v", lane.account.ID, got)
	}
}

func checkOpenHealthRejectsStaleReadyLane(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := reliabilityLane(ctx, "a01", wire.ID{1}, 1, 1)
	defer l.cancel()
	if !l.openHealthy(time.Now()) {
		t.Fatal("fresh authenticated progress must be healthy")
	}
	l.lastProgress.Store(time.Now().Add(-openHealthMaxSilence - time.Second).UnixNano())
	if l.openHealthy(time.Now()) {
		t.Fatal("ready flag alone must not admit OPEN on a stale end-to-end path")
	}
}

func TestOpenOwnerBlackholeFailsOverBeforeProtocolDeadline(t *testing.T) {
	old := openOwnerResultLimit
	openOwnerResultLimit = 25 * time.Millisecond
	defer func() { openOwnerResultLimit = old }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := reliabilityLane(ctx, "a01", wire.ID{1}, 1, 1)
	l.requests = make(chan request, 1)
	defer l.cancel()

	received := make(chan request, 1)
	go func() {
		req := <-l.requests
		received <- req
		<-req.ctx.Done()
	}()

	started := time.Now()
	_, err := l.open(ctx, wire.Connect{Host: "example.invalid", Port: 443})
	if !errors.Is(err, errLaneUnhealthy) {
		t.Fatalf("black-holed owner must be eligible for carrier failover, got %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("black-holed OPEN waited too long before failover")
	}
	select {
	case req := <-received:
		if req.ctx.Err() == nil {
			t.Fatal("abandoned OPEN request context was not canceled")
		}
	default:
		t.Fatal("OPEN request was never submitted")
	}
	if l.openHealthy(time.Now()) {
		t.Fatal("black-holed owner must be quarantined from immediate reselection")
	}
}

func TestClientOpenFailsOverFromBlackholedOwnerToHealthyLane(t *testing.T) {
	old := openOwnerResultLimit
	openOwnerResultLimit = 25 * time.Millisecond
	defer func() { openOwnerResultLimit = old }()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	blackhole := reliabilityLane(ctx, "a01", wire.ID{1}, 1, 1)
	healthy := reliabilityLane(ctx, "a02", wire.ID{1}, 1, 1)
	blackhole.requests = make(chan request, 1)
	healthy.requests = make(chan request, 1)
	defer blackhole.cancel()
	defer healthy.cancel()

	blackholeSeen := make(chan struct{}, 1)
	go func() {
		req := <-blackhole.requests
		blackholeSeen <- struct{}{}
		<-req.ctx.Done()
	}()
	peerDone := make(chan struct{})
	go func() {
		req := <-healthy.requests
		app, peer := pipes()
		peer.Close()
		req.done <- openResult{socket: app}
		close(peerDone)
	}()

	c := &Client{ctx: ctx, lanes: map[string]*lane{"a01": blackhole, "a02": healthy}}
	started := time.Now()
	stream, _, err := c.Open(ctx, wire.Connect{Host: "example.invalid", Port: 443})
	if err != nil {
		t.Fatalf("healthy carrier did not accept OPEN after black-hole failover: %v", err)
	}
	defer stream.Close()
	if time.Since(started) > 500*time.Millisecond {
		t.Fatal("client OPEN failover took too long")
	}
	select {
	case <-blackholeSeen:
	default:
		t.Fatal("test never exercised the black-holed owner")
	}
	select {
	case <-peerDone:
	case <-ctx.Done():
		t.Fatal("healthy owner did not complete OPEN")
	}
	if blackhole.openHealthy(time.Now()) {
		t.Fatal("black-holed lane remained eligible for a subsequent application OPEN")
	}
}

func checkQuotaCleanupControlDoesNotConsumeUrgentQueue(t *testing.T) {
	critical := wire.Frame{Type: wire.GrantUsage}
	if !latencyControl(critical) {
		t.Fatal("usage descriptor must remain latency critical")
	}
	cleanup := wire.Frame{Type: wire.GCStatus}
	if latencyControl(cleanup) {
		t.Fatal("cleanup status must not consume the interactive urgent queue")
	}
	if !maintenanceControl(cleanup) {
		t.Fatal("cleanup status must use the maintenance queue")
	}
}

func checkDiagnosticsDisabledHasNoTraceOrEventHistory(t *testing.T) {
	c := &Client{diagnostics: map[string]*diagnostic{}}
	var snapshot struct {
		Lines  []LineDiagnostic
		Events []string
		Trace  traceStatus
	}
	if err := json.Unmarshal([]byte(c.Diagnostics()), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Trace.Enabled || snapshot.Trace.Written != 0 || len(snapshot.Events) != 0 || len(snapshot.Lines) != 0 {
		t.Fatalf("disabled diagnostics must not retain background history: %+v", snapshot)
	}
}

func TestCleanerRepairDoesNotBlockLaneActor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := &laneMail{dial: func(callCtx context.Context) (*mailbox.Client, error) {
		<-callCtx.Done()
		return nil, callCtx.Err()
	}}
	started := time.Now()
	if err := m.RepairCleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > 100*time.Millisecond {
		t.Fatal("maintenance cleaner repair blocked the lane actor")
	}
	cancel()
	m.repairWG.Wait()
}

func checkMaintenanceTransportFailuresAreDeferredButAccountingFailuresStayFatal(t *testing.T) {
	if !maintenanceRetryable(context.DeadlineExceeded) {
		t.Fatal("maintenance timeout must be retryable without killing the application lane")
	}
	if maintenanceRetryable(errors.New("WALLET_STATE")) {
		t.Fatal("accounting divergence must remain fail-closed")
	}
}

func checkOpenLaneScorePrefersFreshLowRTTOwner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Now()
	fast := reliabilityLane(ctx, "a01", wire.ID{1}, 1, 1)
	slow := reliabilityLane(ctx, "a02", wire.ID{1}, 1, 1)
	defer fast.cancel()
	defer slow.cancel()
	fast.urgentQueue = make(chan queuedControl, 8)
	fast.controlQueue = make(chan queuedControl, 8)
	fast.maintenanceQueue = make(chan queuedControl, 8)
	slow.urgentQueue = make(chan queuedControl, 8)
	slow.controlQueue = make(chan queuedControl, 8)
	slow.maintenanceQueue = make(chan queuedControl, 8)
	fast.rtt.Store(650)
	slow.rtt.Store(2200)
	fast.lastProgress.Store(now.Add(-150 * time.Millisecond).UnixNano())
	slow.lastProgress.Store(now.Add(-100 * time.Millisecond).UnixNano())
	if gotFast, gotSlow := openLaneScore(fast, now), openLaneScore(slow, now); gotFast >= gotSlow {
		t.Fatalf("low-RTT owner was not preferred: fast=%d slow=%d", gotFast, gotSlow)
	}
	for i := 0; i < 5; i++ {
		slow.urgentQueue <- queuedControl{}
	}
	if gotFast, gotSlow := openLaneScore(fast, now), openLaneScore(slow, now); gotFast >= gotSlow {
		t.Fatalf("queued slow owner unexpectedly outranked fast owner: fast=%d slow=%d", gotFast, gotSlow)
	}
}

func checkCompactFrameTraceSamplesRoutineTraffic(t *testing.T) {
	if !compactFrameTrace("frame_rx", wire.Frame{Type: wire.Open}, nil) {
		t.Fatal("OPEN must always remain visible in compact trace")
	}
	c16 := uint64(16)
	c17 := uint64(17)
	if !compactFrameTrace("frame_rx", wire.Frame{Type: wire.Window}, &c16) {
		t.Fatal("routine control sample at counter 16 was dropped")
	}
	if compactFrameTrace("frame_rx", wire.Frame{Type: wire.Window}, &c17) {
		t.Fatal("routine WINDOW chatter was not sampled")
	}
	c32 := uint64(32)
	if !compactFrameTrace("frame_rx", wire.Frame{Type: wire.Data}, &c32) || compactFrameTrace("frame_rx", wire.Frame{Type: wire.Data}, &c17) {
		t.Fatal("DATA trace sampling is not deterministic")
	}
	if !compactFrameTrace("frame_rx", wire.Frame{Type: wire.Data, Payload: []byte{1}}, &c17) {
		t.Fatal("known small DATA lost its latency breadcrumb")
	}
}

// Resource-independent checks form one scenario; lifecycle and concurrency
// checks in this file remain independently scheduled.
func TestScenarioReliability(t *testing.T) {
	t.Log("check=checkStripeDetachDoesNotCascadeCancelOtherOwner")
	checkStripeDetachDoesNotCascadeCancelOtherOwner(t)
	t.Log("check=checkStripeRemoteGrantSelectionSkipsGrantStarvedCarrier")
	checkStripeRemoteGrantSelectionSkipsGrantStarvedCarrier(t)
	t.Log("check=checkOpenHealthRejectsStaleReadyLane")
	checkOpenHealthRejectsStaleReadyLane(t)
	t.Log("check=checkQuotaCleanupControlDoesNotConsumeUrgentQueue")
	checkQuotaCleanupControlDoesNotConsumeUrgentQueue(t)
	t.Log("check=checkDiagnosticsDisabledHasNoTraceOrEventHistory")
	checkDiagnosticsDisabledHasNoTraceOrEventHistory(t)
	t.Log("check=checkMaintenanceTransportFailuresAreDeferredButAccountingFailuresStayFatal")
	checkMaintenanceTransportFailuresAreDeferredButAccountingFailuresStayFatal(t)
	t.Log("check=checkOpenLaneScorePrefersFreshLowRTTOwner")
	checkOpenLaneScorePrefersFreshLowRTTOwner(t)
	t.Log("check=checkCompactFrameTraceSamplesRoutineTraffic")
	checkCompactFrameTraceSamplesRoutineTraffic(t)
}
