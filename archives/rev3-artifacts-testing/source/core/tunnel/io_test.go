package tunnel

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"itom.local/core/mailbox"
	"itom.local/core/wire"
)

type blockingAppendClient struct {
	entered chan struct{}
	release <-chan struct{}
}

func (c *blockingAppendClient) Append(ctx context.Context, _ string, _ []byte) error {
	// entered observes worker concurrency; it must never become part of the
	// blocking behavior itself when a worker processes its next request.
	select {
	case c.entered <- struct{}{}:
	default:
	}
	select {
	case <-c.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (*blockingAppendClient) Close() error { return nil }

func TestAccountRuntimeBoundsQueuedAndInflightDATA(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := make(chan struct{})
	entered := make(chan struct{}, accountDataWorkers)
	r := newAccountRuntime(ctx, []string{"u1", "u2"}, func(context.Context) (appendClient, error) {
		return &blockingAppendClient{entered: entered, release: release}, nil
	})
	defer r.Close()

	payload := make([]byte, 1<<20)
	results := make(chan error, 5)
	for i := 0; i < 5; i++ {
		go func() { results <- r.Append(ctx, "u1", "data", payload) }()
	}
	for i := 0; i < accountDataWorkers; i++ {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("DATA worker did not start")
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		r.mu.Lock()
		outstanding := r.outstanding["u1"]
		pending := r.pendingBytes
		queued := len(r.queues["u1"])
		r.mu.Unlock()
		if outstanding == accountQueuePerUser && pending == accountQueuedMIMEHard && queued == accountQueuePerUser-accountDataWorkers {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("unexpected bounded state: outstanding=%d pending=%d queued=%d", outstanding, pending, queued)
		}
		time.Sleep(time.Millisecond)
	}

	// The fifth caller must still be blocked outside the cloned-runtime budget.
	select {
	case err := <-results:
		t.Fatalf("request completed while both physical writers are blocked: %v", err)
	default:
	}

	close(release)
	for i := 0; i < 5; i++ {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("bounded DATA admission failed to drain")
		}
	}
	snapshot := r.snapshot()
	if snapshot.BusyWorkers != 0 || snapshot.QueuedRequests != 0 || snapshot.PendingBytes != 0 {
		t.Fatalf("runtime did not return to idle: %+v", snapshot)
	}
	if snapshot.Completed != 5 || snapshot.Failed != 0 || snapshot.ServedBytes != 5<<20 || snapshot.UserCompleted["u1"] != 5 || snapshot.UserBytes["u1"] != 5<<20 {
		t.Fatalf("runtime accounting mismatch: %+v", snapshot)
	}
}

func TestAccountRuntimeCloseCancelsInflightWithLiveCaller(t *testing.T) {
	checkAccountRuntimeFailedDialRecovery(t)
	release := make(chan struct{})
	entered := make(chan struct{}, accountDataWorkers)
	r := newAccountRuntime(context.Background(), []string{"u1"}, func(context.Context) (appendClient, error) {
		return &blockingAppendClient{entered: entered, release: release}, nil
	})
	result := make(chan error, 1)
	go func() { result <- r.Append(context.Background(), "u1", "data", make([]byte, 1024)) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("DATA worker did not enter APPEND")
	}
	closed := make(chan struct{})
	go func() { r.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("runtime Close did not cancel in-flight APPEND")
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("shutdown result=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Append caller did not observe runtime shutdown")
	}
}

// Exercise the production dial signature: (*Client)(nil), err becomes a
// non-nil appendClient interface. A later request must redial, and shutdown
// must not Close or Append through that failed result.
func checkAccountRuntimeFailedDialRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	dialErr := errors.New("failed DATA dial")
	release := make(chan struct{})
	close(release)
	var mu sync.Mutex
	dials := 0
	r := newAccountRuntime(ctx, []string{"u1"}, func(context.Context) (appendClient, error) {
		mu.Lock()
		defer mu.Unlock()
		dials++
		if dials == 1 {
			return (*mailbox.Client)(nil), dialErr
		}
		return &blockingAppendClient{entered: make(chan struct{}, 1), release: release}, nil
	})
	defer r.Close()
	if err := r.Append(ctx, "u1", "data", []byte("first")); !errors.Is(err, dialErr) {
		t.Fatalf("failed dial outcome=%v", err)
	}
	if err := r.Append(ctx, "u1", "data", []byte("second")); err != nil {
		t.Fatalf("fresh dial did not recover: %v", err)
	}
	r.Close()
	s := r.snapshot()
	if s.Completed != 1 || s.Failed != 1 || s.PendingBytes != 0 || s.BusyWorkers != 0 || s.QueuedRequests != 0 {
		t.Fatalf("failed dial leaked request ownership: %+v", s)
	}
}

func checkReaderWakeInterruptsIdleBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wake := make(chan struct{}, 1)
	wake <- struct{}{}
	started := time.Now()
	if err := readerPause(ctx, wake, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("reader wake left idle backoff active for %v", elapsed)
	}
}

func checkLatencyControlWakesReader(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := &lane{
		ctx:          ctx,
		readWake:     make(chan struct{}, 1),
		controlQueue: make(chan queuedControl, 1),
		urgentQueue:  make(chan queuedControl, 1),
		receiptQueue: make(chan queuedControl, 1),
	}
	f, err := wire.EncodeControl(1, wire.Connect{Host: "example.invalid", Port: 443})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.SendControl(ctx, f); err != nil {
		t.Fatal(err)
	}
	select {
	case <-l.readWake:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("latency control did not wake reader")
	}
}

func checkReaderPollDelayDemandAndIdleBackoff(t *testing.T) {
	idle := 0
	for i, ms := range []time.Duration{10, 20, 40, 80, 80} {
		if got := readerPollDelay(0, true, false, 0, &idle); got != ms*time.Millisecond {
			t.Fatalf("hot empty step%d delay=%v", i, got)
		}
	}
	idle = 0
	for i, ms := range []time.Duration{20, 40, 80, 80} {
		if got := readerPollDelay(1, false, false, 0, &idle); got != ms*time.Millisecond {
			t.Fatalf("active step%d delay=%v", i, got)
		}
	}
	idle = 0
	want := []time.Duration{250, 500, 1000, 1500, 2000, 2000}
	for i, ms := range want {
		if got := readerPollDelay(0, false, false, 0, &idle); got != ms*time.Millisecond {
			t.Fatalf("idle step %d=%v, want %v", i, got, ms*time.Millisecond)
		}
	}
	if got := readerPollDelay(0, false, true, 1, &idle); got != 0 || idle != 0 {
		t.Fatalf("progress did not reset idle backoff: delay=%v idle=%d", got, idle)
	}
}

func checkAccountRuntimeRoundRobinTake(t *testing.T) {
	r := &accountRuntime{
		ctx:         context.Background(),
		users:       []string{"u1", "u2"},
		deficit:     map[string]int{},
		queues:      map[string][]accountAppend{"u1": {{user: "u1", raw: make([]byte, accountByteQuantum)}, {user: "u1", raw: make([]byte, accountByteQuantum)}}, "u2": {{user: "u2", raw: make([]byte, accountByteQuantum)}, {user: "u2", raw: make([]byte, accountByteQuantum)}}},
		outstanding: map[string]int{"u1": 2, "u2": 2},
	}
	r.cond = sync.NewCond(&r.mu)
	var got []string
	for i := 0; i < 4; i++ {
		req, ok := r.take()
		if !ok {
			t.Fatal("take stopped")
		}
		got = append(got, req.user)
	}
	want := []string{"u1", "u2", "u1", "u2"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("round robin=%v", got)
		}
	}
}

func TestAccountRuntimeAdmissionHonorsCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := make(chan struct{})
	entered := make(chan struct{}, accountDataWorkers)
	r := newAccountRuntime(ctx, []string{"u1"}, func(context.Context) (appendClient, error) {
		return &blockingAppendClient{entered: entered, release: release}, nil
	})
	defer r.Close()

	payload := make([]byte, 1<<20)
	for i := 0; i < accountQueuePerUser; i++ {
		go func() { _ = r.Append(ctx, "u1", "data", payload) }()
	}
	for i := 0; i < accountDataWorkers; i++ {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("DATA worker did not start")
		}
	}

	callCtx, callCancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- r.Append(callCtx, "u1", "data", payload) }()
	callCancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("admission cancellation=%v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("blocked admission ignored caller cancellation")
	}
	close(release)
}

func checkLaneMailReserveDataWriterSkipsBusyConnection(t *testing.T) {
	first := &mailbox.Client{}
	second := &mailbox.Client{}
	m := &laneMail{
		writers:    []*mailbox.Client{first, second},
		writerDead: []bool{false, false},
		writerBusy: []bool{true, false},
	}
	index, writer, reserved, _ := m.reserveDataWriter()
	if index != 1 || writer != second || !reserved {
		t.Fatalf("reserved index=%d writer=%p reserved=%t, want idle writer 1", index, writer, reserved)
	}
	m.releaseDataWriter(index, writer, reserved)
	if m.writerBusy[1] {
		t.Fatal("released writer remained busy")
	}
}

func checkLaneMailCleanupClientPrefersIsolatedCleaner(t *testing.T) {
	base := &mailbox.Client{}
	cleaner := &mailbox.Client{}
	m := &laneMail{Client: base, cleaner: cleaner}
	if got := m.cleanupClient(); got != cleaner {
		t.Fatal("cleanup did not use isolated cleaner")
	}
	m.cleaner = nil
	if got := m.cleanupClient(); got != base {
		t.Fatal("cleanup fallback did not use authenticated base connection")
	}
}

func TestLaneMailActivateCreatesReaderWritersAndCleaner(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cm, _ := tlsMailPair(t, ctx, []string{"itom-up", "itom-down"})
	m, ok := cm.(*laneMail)
	if !ok {
		t.Fatalf("tlsMailPair client=%T, want *laneMail", cm)
	}
	if err := m.Activate(ctx, "itom-down"); err != nil {
		t.Fatal(err)
	}
	if m.reader == nil {
		t.Fatal("reader connection was not isolated")
	}
	if !m.ReaderIdleCapable() {
		t.Fatal("test provider reader did not enable IMAP IDLE")
	}
	if m.cleaner == nil {
		t.Fatal("cleanup connection was not isolated")
	}
	if got := m.DataParallelism(); got != accountDataWorkers {
		t.Fatalf("DATA parallelism=%d, want %d", got, accountDataWorkers)
	}
}

func checkLaneMailReaderConnectionLimitStillEnablesBaseDataFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &laneMail{Client: &mailbox.Client{}}
	m.dial = func(context.Context) (*mailbox.Client, error) {
		return nil, errors.New("IMAP_CONNECT_FAILED")
	}
	if err := m.Activate(ctx, "itom-down"); err != nil {
		t.Fatal(err)
	}
	if m.reader != nil {
		t.Fatal("connection-limit fallback unexpectedly created an isolated reader")
	}
	if !m.baseData || !m.DataAvailable() || m.DataParallelism() != 1 {
		t.Fatalf("reader fallback did not leave usable serialized DATA: base=%t available=%t parallel=%d", m.baseData, m.DataAvailable(), m.DataParallelism())
	}
}

func TestReaderOwnsSelectionIndependentOfCleaner(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cm, _ := tlsMailPair(t, ctx, []string{"itom-up", "itom-down"})
	m := cm.(*laneMail)
	if err := m.Activate(ctx, "itom-down"); err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	_, cur, err := m.Page(ctx, "itom-down", mailbox.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	before, _, _ := m.reader.ReadCommandCounts()
	if _, err = m.cleaner.Tail(ctx, "itom-down"); err != nil {
		t.Fatal(err)
	}
	m.readerCheck = time.Now().Add(-3 * time.Second)
	if _, _, err = m.Page(ctx, "itom-down", cur); err != nil {
		t.Fatal(err)
	}
	after, _, _ := m.reader.ReadCommandCounts()
	if after != before+1 {
		t.Fatal("cleaner supplied receive SELECT", before, after)
	}
	m.cleaner.Close()
	m.readerCheck = time.Time{}
	if _, _, err = m.Page(ctx, "itom-down", cur); err != nil {
		t.Fatal("cleaner failure blocked receiver", err)
	}
}

func checkReaderSelectionProofFreshnessAndValidityBoundaries(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name           string
		expected, seen uint32
		age            time.Duration
		want           bool
	}{
		{"fresh", 4, 4, time.Second, true},
		{"last-nanosecond", 4, 4, 2*time.Second - time.Nanosecond, true},
		{"expired", 4, 4, 2 * time.Second, false},
		{"future", 4, 4, -time.Nanosecond, false},
		{"changed", 4, 5, 0, false},
		{"uninitialized", 0, 0, 0, false},
	} {
		checkScenarioBoundary(t, tc.name, func(t *testing.T) {
			if got := freshReaderSelection(tc.expected, tc.seen, now.Add(-tc.age), now); got != tc.want {
				t.Fatal(got, tc)
			}
		})
	}
}

func TestReaderNeverCreatesIndependentCleanerAudits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cm, _ := tlsMailPair(t, ctx, []string{"itom-up", "itom-down"})
	m := cm.(*laneMail)
	if err := m.Activate(ctx, "itom-down"); err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	before, _, _ := m.cleaner.ReadCommandCounts()
	cur := mailbox.Cursor{}
	for i := 0; i < 3; i++ {
		_, next, err := m.Page(ctx, "itom-down", cur)
		if err != nil {
			t.Fatal(err)
		}
		cur = next
		m.readerCheck = time.Time{}
	}
	after, _, _ := m.cleaner.ReadCommandCounts()
	if after != before {
		t.Fatal("receive initiated maintenance SELECT", before, after)
	}
}

func TestReaderRepairsIdleEOFWithoutResettingControl(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cm, _ := tlsMailPair(t, ctx, []string{"itom-up", "itom-down"})
	m := cm.(*laneMail)
	if err := m.Activate(ctx, "itom-down"); err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	_, cur, err := m.Page(ctx, "itom-down", mailbox.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	old := m.readerRef()
	old.Close()
	raw, _ := mailbox.Encode([]byte("after receive EOF"))
	if err = m.Client.Append(ctx, "itom-down", raw); err != nil {
		t.Fatal(err)
	}
	rows, next, err := m.Page(ctx, "itom-down", cur)
	if err != nil || len(rows) != 1 || next.Validity != cur.Validity || m.readerRef() == old || m.readerRepairs.Load() != 1 {
		t.Fatal("reader recovery", len(rows), next, err)
	}
	previous := m.previousReader.Load()
	if previous == nil || previous.ID != old.SocketSnapshot("receive").ID || previous.State != "dead" || previous.FirstCloseReason == "" {
		t.Fatal("repaired receive lost its first close cause", previous)
	}
	if _, err = m.Client.Ping(ctx); err != nil {
		t.Fatal("receive reset control", err)
	}
}

func checkReaderAllocationProofBounds(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		cur, next            mailbox.Cursor
		uidNext, limit, want uint32
	}{
		{"allocated-page", mailbox.Cursor{Validity: 1}, mailbox.Cursor{Validity: 1}, 18, 16, 16},
		{"unallocated", mailbox.Cursor{Validity: 1}, mailbox.Cursor{Validity: 1}, 1, 16, 0},
		{"absent-proof", mailbox.Cursor{Validity: 1}, mailbox.Cursor{Validity: 1}, 0, 16, 0},
		{"changed", mailbox.Cursor{Validity: 1}, mailbox.Cursor{Validity: 2}, 18, 16, 0},
		{"partial-allocation", mailbox.Cursor{Validity: 1}, mailbox.Cursor{Validity: 1}, 4, 16, 3},
		{"overflow", mailbox.Cursor{Validity: 1, After: ^uint32(0) - 4}, mailbox.Cursor{Validity: 1, After: ^uint32(0) - 4}, ^uint32(0), 16, ^uint32(0) - 1},
		{"terminal", mailbox.Cursor{Validity: 1, After: ^uint32(0)}, mailbox.Cursor{Validity: 1, After: ^uint32(0)}, 1, 16, ^uint32(0)},
	} {
		checkScenarioBoundary(t, tc.name, func(t *testing.T) {
			got := readerAllocatedNext(tc.cur, tc.next, tc.uidNext, tc.limit)
			if got.Validity != tc.next.Validity || got.After != tc.want {
				t.Fatal("cursor exceeded allocation proof", got, tc)
			}
		})
	}
}
func TestReaderOwnAllocationProofPassesAllocatedHoles(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cm, _ := tlsMailPair(t, ctx, []string{"itom-up", "itom-down"})
	m := cm.(*laneMail)
	if err := m.Activate(ctx, "itom-down"); err != nil {
		t.Fatal(err)
	}
	_, cur, err := m.Page(ctx, "itom-down", mailbox.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	before, _, _ := m.reader.ReadCommandCounts()
	raw, err := mailbox.Encode([]byte("allocated-hole-proof"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 33; i++ {
		if err = m.Client.Append(ctx, "itom-down", raw); err != nil {
			t.Fatal(err)
		}
	}
	rows, scanCursor, err := m.cleaner.PageTailLimit(ctx, "itom-down", cur, true, 16)
	if err != nil || len(rows) != 16 {
		t.Fatal("fixture page", len(rows), err)
	}
	for _, row := range rows {
		if err = m.cleaner.Delete(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = m.cleaner.Tail(ctx, "itom-down"); err != nil {
		t.Fatal(err)
	}
	// A second allocated page of holes must also advance before the periodic audit.
	rows, _, err = m.cleaner.PageTailLimit(ctx, "itom-down", scanCursor, true, 16)
	if err != nil || len(rows) != 16 {
		t.Fatal("second fixture page", len(rows), err)
	}
	for _, row := range rows {
		if err = m.cleaner.Delete(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = m.cleaner.Tail(ctx, "itom-down"); err != nil {
		t.Fatal(err)
	}
	if _, err = m.reader.Tail(ctx, "itom-down"); err != nil {
		t.Fatal(err)
	}
	m.readerCheck = time.Now()
	holes, afterHoles, err := m.Page(ctx, "itom-down", cur)
	if err != nil || len(holes) != 0 || afterHoles.After != 4 {
		t.Fatal("reader stuck before allocated holes", afterHoles, len(holes), err)
	}
	holes, afterHoles, err = m.Page(ctx, "itom-down", afterHoles)
	if err != nil || len(holes) != 0 || afterHoles.After != 8 {
		t.Fatal("second allocated range stalled", afterHoles, len(holes), err)
	}
	for afterHoles.After < 32 {
		_, next, err := m.Page(ctx, "itom-down", afterHoles)
		if err != nil || next.After <= afterHoles.After {
			t.Fatal("allocated hole stalled", next, err)
		}
		afterHoles = next
	}
	nextRows, next, err := m.Page(ctx, "itom-down", afterHoles)
	if err != nil || len(nextRows) != 1 || nextRows[0].UID != 33 || next.After != 33 {
		t.Fatal("live tail after holes skipped", next, len(nextRows), err)
	}
	after, _, _ := m.reader.ReadCommandCounts()
	if after != before+1 {
		t.Fatal("reader allocation proof was not owned", before, after)
	}
}

type pollingReaderFixture struct {
	mailbox.Transport
	polled chan struct{}
	idling chan struct{}
}

func (p *pollingReaderFixture) Page(ctx context.Context, _ string, cur mailbox.Cursor) ([]mailbox.Message, mailbox.Cursor, error) {
	select {
	case p.polled <- struct{}{}:
	case <-ctx.Done():
		return nil, cur, ctx.Err()
	}
	return nil, cur, nil
}
func (p *pollingReaderFixture) WaitReadWake(ctx context.Context, _ string, _ time.Duration, _ <-chan struct{}) (bool, bool, bool, error) {
	select {
	case p.idling <- struct{}{}:
	case <-ctx.Done():
		return true, false, false, ctx.Err()
	}
	<-ctx.Done()
	return true, false, false, ctx.Err()
}
func TestReaderPollsActiveSiblingThenIdlesAfterLastStreamCloses(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &pollingReaderFixture{polled: make(chan struct{}, 8), idling: make(chan struct{}, 1)}
	g := newStripeGroup(wire.ID{1}, 1)
	g.active.Store(1)
	l := &lane{ctx: ctx, mail: p, stripe: g, pages: make(chan pageResult, 1), readWake: make(chan struct{}, 1)}
	l.startReader()
	defer func() { cancel(); l.workers.Wait() }()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for i := 0; i < 2; i++ {
		select {
		case <-p.polled:
		case <-p.idling:
			t.Fatal("active sibling entered IDLE")
		case <-deadline.C:
			t.Fatal("reader stopped polling")
		}
		select {
		case <-l.pages:
		case <-deadline.C:
			t.Fatal("missing page result")
		}
	}
	g.active.Store(0)
	for {
		select {
		case <-p.idling:
			return
		case <-l.pages:
		case <-deadline.C:
			t.Fatal("reader did not return to IDLE")
		}
	}
}

// Resource-independent checks form one scenario; lifecycle and concurrency
// checks in this file remain independently scheduled.
func TestScenarioIo(t *testing.T) {
	t.Log("check=checkReaderWakeInterruptsIdleBackoff")
	checkReaderWakeInterruptsIdleBackoff(t)
	t.Log("check=checkLatencyControlWakesReader")
	checkLatencyControlWakesReader(t)
	t.Log("check=checkReaderPollDelayDemandAndIdleBackoff")
	checkReaderPollDelayDemandAndIdleBackoff(t)
	t.Log("check=checkAccountRuntimeRoundRobinTake")
	checkAccountRuntimeRoundRobinTake(t)
	t.Log("check=checkLaneMailReserveDataWriterSkipsBusyConnection")
	checkLaneMailReserveDataWriterSkipsBusyConnection(t)
	t.Log("check=checkLaneMailCleanupClientPrefersIsolatedCleaner")
	checkLaneMailCleanupClientPrefersIsolatedCleaner(t)
	t.Log("check=checkLaneMailReaderConnectionLimitStillEnablesBaseDataFallback")
	checkLaneMailReaderConnectionLimitStillEnablesBaseDataFallback(t)
	t.Log("check=checkReaderSelectionProofFreshnessAndValidityBoundaries")
	checkReaderSelectionProofFreshnessAndValidityBoundaries(t)
	t.Log("check=checkReaderAllocationProofBounds")
	checkReaderAllocationProofBounds(t)
}

func TestReaderNotificationDoesNotAddSelectionRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cm, _ := tlsMailPair(t, ctx, []string{"up", "down"})
	m := cm.(*laneMail)
	if err := m.Activate(ctx, "down"); err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	_, cur, err := m.Page(ctx, "down", mailbox.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	checked := m.readerCheck
	before, _, _ := m.readerRef().ReadCommandCounts()
	raw, _ := mailbox.Encode([]byte("new row"))
	if err := m.Client.Append(ctx, "down", raw); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = m.receiveWait(ctx, "down", 100*time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows, _, err := m.Page(ctx, "down", cur)
	if err != nil || len(rows) != 1 {
		t.Fatal("notified row lost", err)
	}
	after, _, _ := m.readerRef().ReadCommandCounts()
	if after != before || m.readerCheck != checked {
		t.Fatal("EXISTS forced another SELECT", before, after)
	}
}

// Client cold-start avoids blocking readiness on its optional second writer.
// The second slot must never be counted ready before its real authenticated
// dial succeeds, and the existing repair worker must fill it asynchronously.
func TestLaneMailClientFastStartDefersSecondDATA(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cm, _ := tlsMailPair(t, ctx, []string{"itom-up", "itom-down"})
	m := cm.(*laneMail)
	m.stageSecondDataWriter = true
	if err := m.Activate(ctx, "itom-down"); err != nil {
		t.Fatal(err)
	}
	if m.reader == nil || m.cleaner == nil || !m.DataAvailable() {
		t.Fatal("fast-start published without reader, cleaner or first DATA writer")
	}
	m.writerMu.Lock()
	first := len(m.writers) == accountDataWorkers && m.writers[0] != nil && m.writers[1] == nil && m.writerDead[1]
	m.writerMu.Unlock()
	if !first {
		t.Fatal("optional DATA writer was dialed or incorrectly counted as live during cold-start")
	}
	deadline := time.Now().Add(5 * time.Second)
	for m.DataParallelism() != accountDataWorkers && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if m.DataParallelism() != accountDataWorkers {
		t.Fatal("asynchronous optional DATA capacity was not restored")
	}
}

// Closing while optional capacity is still deferred must wait for its repair
// goroutine and must not resurrect a DATA socket after the transport stops.
func TestLaneMailClientFastStartShutdownDuringDeferredDial(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cm, _ := tlsMailPair(t, ctx, []string{"itom-up", "itom-down"})
	m := cm.(*laneMail)
	m.stageSecondDataWriter = true
	if err := m.Activate(ctx, "itom-down"); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if m.DataAvailable() {
		t.Fatal("closed staged account exposed phantom DATA capacity")
	}
}
