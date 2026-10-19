package tunnel

import (
	"bytes"
	"context"
	"errors"
	"io"
	"itom.local/core/datapath"
	"itom.local/core/flow"
	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memoryMail struct {
	mu   sync.Mutex
	rows map[string]map[uint32][]byte
	next map[string]uint32
}

func (m *memoryMail) Ensure(ctx context.Context, f string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rows[f] == nil {
		m.rows[f] = map[uint32][]byte{}
	}
	return ctx.Err()
}
func (m *memoryMail) Append(ctx context.Context, f string, b []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	m.next[f]++
	m.rows[f][m.next[f]] = append([]byte(nil), b...)
	return nil
}
func (m *memoryMail) Tail(ctx context.Context, f string) (mailbox.Cursor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return mailbox.Cursor{}, err
	}
	return mailbox.Cursor{Validity: 1, After: m.next[f]}, nil
}
func (m *memoryMail) Recent(ctx context.Context, f string, limit int) ([]mailbox.Message, mailbox.Cursor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, mailbox.Cursor{}, err
	}
	if limit < 1 {
		return nil, mailbox.Cursor{}, mailbox.ErrLimit
	}
	end := m.next[f]
	start := uint32(1)
	if end >= uint32(limit) {
		start = end - uint32(limit) + 1
	}
	rows := make([]mailbox.Message, 0, limit)
	for id := start; id <= end && end != 0; id++ {
		if b, ok := m.rows[f][id]; ok {
			rows = append(rows, mailbox.Message{Folder: f, Validity: 1, UID: id, MIME: append([]byte(nil), b...)})
		}
	}
	return rows, mailbox.Cursor{Validity: 1, After: end}, nil
}
func (m *memoryMail) Page(ctx context.Context, f string, c mailbox.Cursor) ([]mailbox.Message, mailbox.Cursor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return nil, c, e
	}
	var rows []mailbox.Message
	end := m.next[f]
	if end > c.After+16 {
		end = c.After + 16
	}
	for id := c.After + 1; id <= end; id++ {
		if b, ok := m.rows[f][id]; ok {
			rows = append(rows, mailbox.Message{Folder: f, Validity: 1, UID: id, MIME: append([]byte(nil), b...)})
		}
	}
	return rows, mailbox.Cursor{Validity: 1, After: end}, nil
}
func (m *memoryMail) Delete(ctx context.Context, r mailbox.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows[r.Folder], r.UID)
	return ctx.Err()
}
func (m *memoryMail) Absent(ctx context.Context, f string, v, id uint32) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.rows[f][id]
	return !ok, ctx.Err()
}

func TestRecoveryEmptyMailboxUnwedgesSaturatedLegacyControlJournal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m := &memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}
	cfg, _ := servercfg.New("test")
	cfg.Accounts = []servercfg.Account{{ID: "a01", Mail: mailbox.Config{Host: "mail.example", Port: 993, TLS: "tls", Username: "user", Password: "test"}}}
	u, err := cfg.AddUser("test")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{u.Up, u.Down} {
		if err = m.Ensure(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	s, err := ledger(dir, "control", cfg.Accounts[0], u, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.EnableAfterInventory("a01", u.ID, s.Run()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 128; i++ {
		g, issueErr := s.Issue(journal.Scope{Account: "a01", User: u.ID, Session: wire.ID{1}}, pool.Amount{Bytes: 128, Messages: 1})
		if issueErr != nil {
			t.Fatalf("issue %d: %v", i, issueErr)
		}
		a, prepErr := s.Prepare(g.Ref, u.Down, wire.ID{byte(i + 1)}, 128, []byte("legacy-preauth-control"))
		if prepErr != nil {
			t.Fatalf("prepare %d: %v", i, prepErr)
		}
		if _, err = s.Start(a.Ref); err != nil {
			t.Fatal(err)
		}
		if err = s.Complete(a.Ref, journal.Uncertain); err != nil {
			t.Fatal(err)
		}
		if err = s.CloseGrant(g.Ref); err != nil {
			t.Fatal(err)
		}
	}
	held, err := s.Held("a01", u.ID)
	if err != nil || held.Messages != 128 {
		t.Fatalf("legacy journal not saturated: %+v %v", held, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = ledger(dir, "control", cfg.Accounts[0], u, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = recoverLocal(ctx, m, s, u.Down); err != nil {
		t.Fatal(err)
	}
	held, err = s.Held("a01", u.ID)
	if err != nil || held.Messages != 0 || held.Bytes != 0 {
		t.Fatalf("empty mailbox did not release orphan physical debt: %+v %v", held, err)
	}
}

func checkPreauthAppendDoesNotConsumeRuntimeControlJournal(t *testing.T) {
	ctx := context.Background()
	m := &memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}
	if err := m.Ensure(ctx, "tx"); err != nil {
		t.Fatal(err)
	}
	if err := appendEphemeralControl(ctx, m, "tx", []byte("ITC9-test")); err != nil {
		t.Fatal(err)
	}
	rows, _, err := m.Page(ctx, "tx", mailbox.Cursor{})
	if err != nil || len(rows) != 1 {
		t.Fatalf("preauth append missing: %d %v", len(rows), err)
	}
	body, err := mailbox.Decode(rows[0].MIME)
	if err != nil || !bytes.Equal(body, []byte("ITC9-test")) {
		t.Fatalf("preauth body mismatch: %q %v", body, err)
	}
}

func TestAuthenticatedRuntimeTCP(t *testing.T) {
	t.Run("memory", func(t *testing.T) { testRuntimeTCP(t, false) })
	t.Run("TLS_IMAP", func(t *testing.T) { testRuntimeTCP(t, true) })
	t.Run("TLS_IMAP_latency", func(t *testing.T) { testRuntimeTCP(t, true) })
}
func TestStreamLimitRotatesLane(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := &lane{ctx: ctx, cancel: cancel, nextID: maxStripeStreamID + 2, requests: make(chan request, 1)}
	l.ready.Store(true)
	done := make(chan openResult, 1)
	l.requests <- request{ctx: ctx, done: done}
	if e := l.run(); e == nil || e.Error() != "LANE_ROTATION_REQUIRED" {
		t.Fatalf("expected supervisor rotation, got %v", e)
	}
	if l.ready.Load() || ctx.Err() == nil {
		t.Fatal("exhausted lane must stop and withdraw readiness")
	}
	if result := <-done; result.err == nil {
		t.Fatal("caller must receive a retryable open failure")
	}
}

type idleMemoryMail struct {
	*memoryMail
	pages atomic.Int64
	waits atomic.Int64
}

func (m *idleMemoryMail) Page(ctx context.Context, folder string, cur mailbox.Cursor) ([]mailbox.Message, mailbox.Cursor, error) {
	m.pages.Add(1)
	return m.memoryMail.Page(ctx, folder, cur)
}

func (m *idleMemoryMail) WaitRead(ctx context.Context, _ string, _ time.Duration) (bool, bool, error) {
	m.waits.Add(1)
	<-ctx.Done()
	return true, false, ctx.Err()
}

func (m *idleMemoryMail) WaitReadWake(ctx context.Context, _ string, _ time.Duration, wake <-chan struct{}) (bool, bool, bool, error) {
	m.waits.Add(1)
	select {
	case <-ctx.Done():
		return true, false, false, ctx.Err()
	case <-wake:
		return true, false, true, nil
	}
}

func TestReaderUsesIdleInsteadOfEmptyPollingLoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	base := &memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}
	if err := base.Ensure(ctx, "rx"); err != nil {
		t.Fatal(err)
	}
	m := &idleMemoryMail{memoryMail: base}
	l := &lane{ctx: ctx, cancel: cancel, mail: m, recvFolder: "rx", recvCursor: mailbox.Cursor{Validity: 1}, pages: make(chan pageResult, 4)}
	l.startReader()
	select {
	case got := <-l.pages:
		if got.err != nil || len(got.rows) != 0 {
			t.Fatalf("initial page rows=%d err=%v", len(got.rows), got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("reader did not perform initial tail check")
	}
	deadline := time.Now().Add(time.Second)
	for m.waits.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if m.waits.Load() != 1 {
		t.Fatalf("IDLE waits=%d, want 1", m.waits.Load())
	}
	time.Sleep(30 * time.Millisecond)
	if got := m.pages.Load(); got != 1 {
		t.Fatalf("reader polled while IDLE was active: pages=%d", got)
	}
	cancel()
	l.workers.Wait()
}

func TestLatencyControlKicksActiveIdleAndEntersHotPolling(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	base := &memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}
	if err := base.Ensure(ctx, "rx"); err != nil {
		t.Fatal(err)
	}
	m := &idleMemoryMail{memoryMail: base}
	l := &lane{
		ctx:          ctx,
		cancel:       cancel,
		mail:         m,
		recvFolder:   "rx",
		recvCursor:   mailbox.Cursor{Validity: 1},
		pages:        make(chan pageResult, 32),
		readWake:     make(chan struct{}, 1),
		controlQueue: make(chan queuedControl, 1),
		urgentQueue:  make(chan queuedControl, 1),
		receiptQueue: make(chan queuedControl, 1),
	}
	l.startReader()
	select {
	case got := <-l.pages:
		if got.err != nil || len(got.rows) != 0 {
			t.Fatalf("initial page rows=%d err=%v", len(got.rows), got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("reader did not perform initial tail check")
	}
	deadline := time.Now().Add(time.Second)
	for m.waits.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if m.waits.Load() == 0 {
		t.Fatal("reader never entered IDLE")
	}
	f, err := wire.EncodeControl(0, wire.Echo{Token: 1})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := l.SendControl(ctx, f); err != nil {
		t.Fatal(err)
	}
	// The active IDLE must be interrupted immediately and the reader must perform
	// another Page while the hot response window is still active.
	for m.pages.Load() < 2 && time.Since(started) < 500*time.Millisecond {
		time.Sleep(time.Millisecond)
	}
	if m.pages.Load() < 2 {
		t.Fatal("latency control did not kick active IDLE")
	}
	if time.Now().UnixNano() >= l.readHotUntil.Load() {
		t.Fatal("reader hot window was not armed")
	}
	// During that window the reader should not immediately re-enter IDLE.
	waits := m.waits.Load()
	time.Sleep(20 * time.Millisecond)
	if got := m.waits.Load(); got != waits {
		t.Fatalf("reader re-entered IDLE during hot response window: waits %d -> %d", waits, got)
	}
	cancel()
	l.workers.Wait()
}

func TestReaderStartsAfterAcceptedHandshakeUID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}
	const folder = "rx"
	if err := m.Ensure(ctx, folder); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := m.Append(ctx, folder, []byte{byte(i + 1)}); err != nil {
			t.Fatal(err)
		}
	}
	l := &lane{ctx: ctx, cancel: cancel, mail: m, recvFolder: folder, recvCursor: mailbox.Cursor{Validity: 1, After: 2}, pages: make(chan pageResult, 4)}
	l.startReader()
	select {
	case got := <-l.pages:
		if got.err != nil {
			t.Fatal(got.err)
		}
		if len(got.rows) != 1 || got.rows[0].UID != 3 {
			t.Fatalf("reader replayed pre-handshake history: %+v", got.rows)
		}
	case <-time.After(time.Second):
		t.Fatal("reader did not start from accepted handshake cursor")
	}
	cancel()
	l.workers.Wait()
}

func testRuntimeTCP(t *testing.T, tlsIMAP bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	m := &memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}
	cfg, _ := servercfg.New("test")
	cfg.Accounts = []servercfg.Account{{ID: "a01", Mail: mailbox.Config{Host: "mail.example", Port: 993, TLS: "tls", Username: "user", Password: "test"}}}
	u, e := cfg.AddUser("test")
	if e != nil {
		t.Fatal(e)
	}
	var clientMail, serverMail mailbox.Transport = m, m
	if tlsIMAP {
		clientMail, serverMail = tlsMailPair(t, ctx, []string{u.Up, u.Down})
	}
	listener, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	go func() {
		c, e := listener.Accept()
		if e == nil {
			defer c.Close()
			io.Copy(c, c)
		}
	}()
	failures := make(chan error, 2)
	serverDir := t.TempDir()
	go func() {
		l, e := newLane(ctx, cfg.Server, u, cfg.Accounts[0], serverDir, true, serverMail)
		if e == nil {
			l.allowPrivate = true
			e = l.run()
		}
		failures <- e
	}()
	client, e := newLane(ctx, cfg.Server, u, cfg.Accounts[0], t.TempDir(), false, clientMail)
	if e != nil {
		t.Fatal(e)
	}
	go func() { failures <- client.run() }()
	for !client.ready.Load() {
		select {
		case e := <-failures:
			t.Fatal(e)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	n, _ := strconv.Atoi(port)
	c, e := client.open(ctx, wire.Connect{Host: "127.0.0.1", Port: uint16(n)})
	if e != nil {
		select {
		case f := <-failures:
			t.Fatalf("open %v / runtime %v", e, f)
		default:
			t.Fatal(e)
		}
	}
	defer c.Close()
	size := 20000
	if tlsIMAP && !strings.Contains(t.Name(), "latency") {
		size = 160000
	}
	payload := bytes.Repeat([]byte("ITOM-real-TCP/"), size)
	t.Logf("bidirectional payload: %d bytes per direction", len(payload))
	go func() {
		_, e := c.Write(payload)
		if e == nil {
			e = c.CloseWrite()
		}
		if e != nil {
			failures <- e
		}
	}()
	dataDone := make(chan []byte, 1)
	go func() { b, _ := io.ReadAll(c); dataDone <- b }()
	select {
	case b := <-dataDone:
		if !bytes.Equal(b, payload) {
			select {
			case cause := <-failures:
				t.Fatalf("received %d want %d; runtime: %v", len(b), len(payload), cause)
			case <-time.After(200 * time.Millisecond):
				t.Fatalf("received %d want %d", len(b), len(payload))
			}
		}
	case e := <-failures:
		t.Fatal(e)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	cancel()
	for i := 0; i < 2; i++ {
		select {
		case <-failures:
		case <-time.After(5 * time.Second):
			t.Fatal("runtime did not join")
		}
	}
}

func TestS1RemotePhysicalBatchAdmissionIsBounded(t *testing.T) {
	// This body is larger than the pre-S1 single-record gate but remains well
	// inside the bounded S1 ITD3 + ITQ1 envelope. Without the S1 gate fix read()
	// silently discards it and the sender eventually fills its logical window.
	body := make([]byte, wire.MaxRecord+257)
	copy(body, []byte("ITQ1"))
	mime, err := mailbox.Encode(body)
	if err != nil {
		t.Fatal(err)
	}
	l := &lane{side: true, recvFolder: "up", reports: map[[32]byte]wire.UsageReport{}, pending: map[[32]byte]mailbox.Message{}}
	if err = l.read([]mailbox.Message{{Folder: "up", Validity: 1, UID: 1, MIME: mime}}); err != nil {
		t.Fatal(err)
	}
	if len(l.pending) != 1 {
		t.Fatal("bounded multi-record physical candidate was discarded")
	}

	// The larger S1 allowance is still finite. A body beyond the exact ITD3
	// maximum plus the conservative ITQ1 metadata allowance is ignored before
	// it can enter pending descriptor state.
	tooLarge := make([]byte, wire.MaxDataBatchBody+257)
	copy(tooLarge, []byte("ITQ1"))
	mime, err = mailbox.Encode(tooLarge)
	if err != nil {
		t.Fatal(err)
	}
	if err = l.read([]mailbox.Message{{Folder: "up", Validity: 1, UID: 2, MIME: mime}}); err != nil {
		t.Fatal(err)
	}
	if len(l.pending) != 1 {
		t.Fatal("oversized physical candidate entered pending state")
	}
	if pendingDataMIMEHardCap/dataLeaseBytes != 6 || dataLeaseMessages != 32 {
		t.Fatal("12 MiB DATA journal must retain six 2 MiB cumulative thirty-two-message lease units")
	}
	if fastLeaseBytes != dataBatchMIMEHardCap || fastLeaseMessages != 32 || remoteBulkLeases != 2 {
		t.Fatal("fast lease must be 512 KiB/32 messages with two unchanged bulk leases")
	}
	if fastLeaseBytes+remoteBulkLeases*dataLeaseBytes > pendingDataMIMEHardCap-localFastReserveBytes {
		t.Fatal("remote leases consumed local fast reserve")
	}
}

func TestRemoteGrantReserveProtectsInteractiveDATA(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := &lane{ctx: ctx, grants: make(chan wire.QuotaKey, 4), fastGrants: make(chan wire.QuotaKey, fastGrantReserve)}
	fast1 := wire.QuotaKey{Grant: 1}
	bulk := wire.QuotaKey{Grant: 2}
	l.fastGrants <- fast1
	l.grants <- bulk

	got, err := l.takeRemoteGrant(false)
	if err != nil || got.Grant != bulk.Grant {
		t.Fatalf("bulk consumed reserved grant: got=%v err=%v", got.Grant, err)
	}
	got, err = l.takeRemoteGrant(true)
	if err != nil || got.Grant != fast1.Grant {
		t.Fatalf("interactive DATA missed reserved grant: got=%v err=%v", got.Grant, err)
	}

	// Interactive DATA may borrow spare bulk capacity, but bulk must never borrow
	// the final latency reserve while it is waiting for physical cleanup. The
	// fast pool is already empty after the previous take.
	borrow := wire.QuotaKey{Grant: 3}
	l.grants <- borrow
	got, err = l.takeRemoteGrant(true)
	if err != nil || got.Grant != borrow.Grant {
		t.Fatalf("interactive DATA failed to borrow bulk grant: got=%v err=%v", got.Grant, err)
	}

	reserved := wire.QuotaKey{Grant: 4}
	l.fastGrants <- reserved
	done := make(chan error, 1)
	go func() {
		_, e := l.takeRemoteGrant(false)
		done <- e
	}()
	select {
	case <-done:
		t.Fatal("bulk consumed latency reserve")
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	select {
	case e := <-done:
		if !errors.Is(e, context.Canceled) {
			t.Fatalf("bulk wait cancellation: %v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("bulk grant waiter did not stop")
	}
}

func checkCumulativeRemoteGrantLeaseRequeuesUntilFinalOrdinal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	key := wire.QuotaKey{Grant: 7}
	l := &lane{
		ctx:         ctx,
		grants:      make(chan wire.QuotaKey, 4),
		fastGrants:  make(chan wire.QuotaKey, fastGrantReserve),
		grantLimits: map[wire.QuotaKey]wire.Quota{key: {Bytes: dataLeaseBytes, Messages: dataLeaseMessages}},
		grantFast:   map[wire.QuotaKey]bool{key: false},
	}
	reused, err := l.requeueRemoteGrant(key, wire.Quota{Bytes: dataBatchMIMEHardCap, Messages: 1})
	if err != nil || !reused {
		t.Fatalf("first cumulative ordinal was not recycled: reused=%v err=%v", reused, err)
	}
	if got := <-l.grants; got != key {
		t.Fatal("recycled a different grant")
	}
	reused, err = l.requeueRemoteGrant(key, wire.Quota{Bytes: dataLeaseBytes, Messages: dataLeaseMessages})
	if err != nil || reused {
		t.Fatalf("exhausted lease was recycled: reused=%v err=%v", reused, err)
	}
	select {
	case <-l.grants:
		t.Fatal("exhausted lease returned to bulk pool")
	default:
	}
}

func checkBulkLeaseRequeuesPartialByteRemainderForExactSpend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	key := wire.QuotaKey{Grant: 8}
	l := &lane{
		ctx:         ctx,
		grants:      make(chan wire.QuotaKey, 4),
		fastGrants:  make(chan wire.QuotaKey, fastGrantReserve),
		grantLimits: map[wire.QuotaKey]wire.Quota{key: {Bytes: dataLeaseBytes, Messages: dataLeaseMessages}},
		grantFast:   map[wire.QuotaKey]bool{key: false},
	}
	// A partial byte remainder is still useful for an ordinary small physical
	// batch. Selection may return it; Wallet.Spend classifies an exact oversize as
	// WALLET_EXHAUSTED without committing, after which the writer fences and
	// retries the same unsent encrypted batch on the next grant.
	total := wire.Quota{Bytes: dataLeaseBytes - dataBatchMIMEHardCap + 1, Messages: 4}
	reused, err := l.requeueRemoteGrant(key, total)
	if err != nil || !reused {
		t.Fatalf("partial bulk remainder was discarded: reused=%v err=%v", reused, err)
	}
	if got := <-l.grants; got != key {
		t.Fatal("requeued a different grant")
	}
}
func checkCumulativeRemoteDescriptorBindsExactOrdinal(t *testing.T) {
	key := wire.QuotaKey{Store: wire.ID{1}, Epoch: 2, Grant: 3, Session: wire.ID{4}, User: "12345678", Account: "a01"}
	grant := grantRef(key)
	attempts := []journal.Attempt{
		{Ref: journal.AttemptRef{Grant: grant, ID: 11}, Ordinal: 2},
		{Ref: journal.AttemptRef{Grant: grant, ID: 12}, Ordinal: 3},
	}

	// Ordinal 1 has already been reclaimed. A later cumulative descriptor must
	// still bind to its own durable attempt instead of requiring the first row.
	if got := remoteAttemptRef(attempts, key, 2); got.ID != 11 {
		t.Fatalf("ordinal 2 bound to attempt %d", got.ID)
	}
	if got := remoteAttemptRef(attempts, key, 3); got.ID != 12 {
		t.Fatalf("ordinal 3 bound to attempt %d", got.ID)
	}
	if got := remoteAttemptRef(attempts, key, 1); got.ID != 0 {
		t.Fatalf("missing ordinal 1 unexpectedly bound to attempt %d", got.ID)
	}
	attempts = append(attempts, journal.Attempt{Ref: journal.AttemptRef{Grant: grant, ID: 17}, Ordinal: 17})
	if got := remoteAttemptRef(attempts, key, 17); got.ID != 17 {
		t.Fatalf("high cumulative ordinal bound to attempt %d", got.ID)
	}
}

func checkFastRemoteLeaseSurvivesProbeBurstAndStopsOnBytes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	key := wire.QuotaKey{Grant: 77}
	l := &lane{
		ctx:         ctx,
		grants:      make(chan wire.QuotaKey, 4),
		fastGrants:  make(chan wire.QuotaKey, fastGrantReserve),
		grantLimits: map[wire.QuotaKey]wire.Quota{key: {Bytes: fastLeaseBytes, Messages: fastLeaseMessages}},
		grantFast:   map[wire.QuotaKey]bool{key: true},
	}
	for ordinal := uint64(1); ordinal <= 8; ordinal++ {
		reused, err := l.requeueRemoteGrant(key, wire.Quota{Bytes: ordinal * 4096, Messages: ordinal})
		if err != nil || !reused {
			t.Fatalf("fast ordinal %d was not recycled: reused=%v err=%v", ordinal, reused, err)
		}
		if got := <-l.fastGrants; got != key {
			t.Fatal("fast lease returned to wrong queue")
		}
	}
	reused, err := l.requeueRemoteGrant(key, wire.Quota{Bytes: fastLeaseBytes, Messages: 9})
	if err != nil || reused {
		t.Fatalf("byte-exhausted fast lease recycled: reused=%v err=%v", reused, err)
	}
}

func checkCumulativeGrantCleanupRequiresClosedCompleteStatus(t *testing.T) {
	key := wire.QuotaKey{Grant: 9}
	partial := wire.CleanupStatus{Key: key, Used: wire.Quota{Bytes: 1000, Messages: 2}, Registered: wire.Quota{Bytes: 1000, Messages: 2}, Released: wire.Quota{Bytes: 500, Messages: 1}}
	if grantFullyReleased(partial) {
		t.Fatal("partial cumulative cleanup was treated as final")
	}
	partial.Closed = true
	partial.Released = partial.Used
	if !grantFullyReleased(partial) {
		t.Fatal("closed fully released cumulative grant was not final")
	}
}

func checkRuntimeFlowWindowKeepsInteractiveHeadroom(t *testing.T) {
	if runtimeLogicalBytes != 32<<20 || runtimeAccountBytes != 32<<20 || runtimeReceiveBytes != 32<<20 {
		t.Fatal("one session must share the bounded 32 MiB budget")
	}
	if runtimeStreamWindow != 1<<20 {
		t.Fatalf("runtime stream window=%d, want 1 MiB initial", runtimeStreamWindow)
	}
	if runtimeReceiveBytes-flow.MaxStreamWindow < runtimeInteractiveReserve {
		t.Fatal("maximum adaptive stream window leaves insufficient shared receive headroom")
	}
}

func checkPerf3BulkTargetFitsPhysicalGrant(t *testing.T) {
	// The collector can cross its target by at most one MaxData record. Include
	// worst-case per-record Noise/frame length and bounded ITD3/ITQ1 metadata,
	// then run the exact MIME encoder. This couples the perf3 batch target to the
	// 512 KiB physical permit so ordinary batching cannot fail after durable spend.
	decoded := datapath.BulkBatchTargetBytes + wire.MaxData + wire.MaxDataBatchRecords*(2*wire.HeaderSize+wire.FrameHeaderSize+wire.TagSize+4) + 256
	mime, err := mailbox.Encode(make([]byte, decoded))
	if err != nil {
		t.Fatal(err)
	}
	if len(mime) > dataBatchMIMEHardCap {
		t.Fatalf("bulk target can exceed physical permit: decoded=%d mime=%d cap=%d", decoded, len(mime), dataBatchMIMEHardCap)
	}
	if pendingDataMIMEHardCap/dataLeaseBytes != 6 || (pendingDataMIMEHardCap/dataLeaseBytes)*dataLeaseMessages != 192 {
		t.Fatal("12 MiB total byte exposure must retain six 2 MiB leases with one hundred ninety-two bounded ordinals")
	}
	if remoteBulkLeases != 2 || remoteBulkLeases*dataLeaseMessages != 64 {
		t.Fatal("remote bulk admission must retain two thirty-two-message leases")
	}
	if fastLeaseBytes+remoteBulkLeases*dataLeaseBytes != (9 << 19) {
		t.Fatal("remote reservation must be 4.5 MiB")
	}
}

func checkLegacyAndCurrentPreauthBodiesAreRecoveryGarbage(t *testing.T) {
	ctx := context.Background()
	m := &memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}
	if err := m.Ensure(ctx, "mail"); err != nil {
		t.Fatal(err)
	}
	for _, body := range [][]byte{
		[]byte("ITP8{}"), []byte("ITC8{}"), []byte("ITH8{}"),
		[]byte("ITP9{}"), []byte("ITC9{}"), []byte("ITH9{}"),
		[]byte("ITB1-runtime"),
	} {
		if err := m.Append(ctx, "mail", mustEncodeMailbox(t, body)); err != nil {
			t.Fatal(err)
		}
	}
	rows, _, err := m.Page(ctx, "mail", mailbox.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	stale := stalePreauthRows(rows)
	if len(stale) != 6 {
		t.Fatalf("stale preauth rows=%d, want 6", len(stale))
	}
	for _, row := range stale {
		body, err := mailbox.Decode(row.MIME)
		if err != nil {
			t.Fatal(err)
		}
		if !isPreauthBody(body) {
			t.Fatalf("non-preauth row selected: %q", body)
		}
	}
}

func mustEncodeMailbox(t *testing.T, body []byte) []byte {
	t.Helper()
	raw, err := mailbox.Encode(body)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func checkAutomaticJanitorDeletesOnlyPreauthGarbage(t *testing.T) {
	ctx := context.Background()
	m := &memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}
	const folder = "mail"
	if err := m.Ensure(ctx, folder); err != nil {
		t.Fatal(err)
	}
	preauth := mustEncodeMailbox(t, []byte("ITH9-stale-handshake"))
	runtime := mustEncodeMailbox(t, []byte("ITB1-authenticated-runtime"))
	if err := m.Append(ctx, folder, preauth); err != nil {
		t.Fatal(err)
	}
	if err := m.Append(ctx, folder, runtime); err != nil {
		t.Fatal(err)
	}
	removed, err := cleanupAutomaticFolder(ctx, m, folder)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("automatic cleanup removed=%d want=1", removed)
	}
	rows, _, err := m.Page(ctx, folder, mailbox.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("automatic cleanup destroyed authenticated mail: rows=%d", len(rows))
	}
	body, err := mailbox.Decode(rows[0].MIME)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, []byte("ITB1-authenticated-runtime")) {
		t.Fatalf("wrong row retained: %q", body)
	}
}

// Resource-independent checks form one scenario; lifecycle and concurrency
// checks in this file remain independently scheduled.
func TestScenarioLane(t *testing.T) {
	t.Log("check=checkPreauthAppendDoesNotConsumeRuntimeControlJournal")
	checkPreauthAppendDoesNotConsumeRuntimeControlJournal(t)
	t.Log("check=checkCumulativeRemoteGrantLeaseRequeuesUntilFinalOrdinal")
	checkCumulativeRemoteGrantLeaseRequeuesUntilFinalOrdinal(t)
	t.Log("check=checkBulkLeaseRequeuesPartialByteRemainderForExactSpend")
	checkBulkLeaseRequeuesPartialByteRemainderForExactSpend(t)
	t.Log("check=checkCumulativeRemoteDescriptorBindsExactOrdinal")
	checkCumulativeRemoteDescriptorBindsExactOrdinal(t)
	t.Log("check=checkFastRemoteLeaseSurvivesProbeBurstAndStopsOnBytes")
	checkFastRemoteLeaseSurvivesProbeBurstAndStopsOnBytes(t)
	t.Log("check=checkCumulativeGrantCleanupRequiresClosedCompleteStatus")
	checkCumulativeGrantCleanupRequiresClosedCompleteStatus(t)
	t.Log("check=checkRuntimeFlowWindowKeepsInteractiveHeadroom")
	checkRuntimeFlowWindowKeepsInteractiveHeadroom(t)
	t.Log("check=checkPerf3BulkTargetFitsPhysicalGrant")
	checkPerf3BulkTargetFitsPhysicalGrant(t)
	t.Log("check=checkLegacyAndCurrentPreauthBodiesAreRecoveryGarbage")
	checkLegacyAndCurrentPreauthBodiesAreRecoveryGarbage(t)
	t.Log("check=checkAutomaticJanitorDeletesOnlyPreauthGarbage")
	checkAutomaticJanitorDeletesOnlyPreauthGarbage(t)
}
