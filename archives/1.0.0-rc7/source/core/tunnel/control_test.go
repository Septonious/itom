package tunnel

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"itom.local/core/mailbox"
	"itom.local/core/secure"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
	"strings"
	"testing"
	"time"
)

func checkControlEnvelopeBounds(t *testing.T) {
	for _, raw := range [][]byte{[]byte("ITB1"), append([]byte("ITB1"), 0, 0, 0, 255), append([]byte("ITB1"), 255, 255, 255, 255)} {
		if _, _, e := unpackControl(raw); e == nil {
			t.Fatal("accepted malformed envelope")
		}
	}
	raw := []byte("ITB1")
	for i := 0; i < controlBatch; i++ {
		raw = binary.BigEndian.AppendUint32(raw, wire.HeaderSize)
		raw = append(raw, make([]byte, wire.HeaderSize)...)
	}
	if got, control, e := unpackControl(raw); e != nil || !control || len(got) != 16 {
		t.Fatal(len(got), e)
	}
	raw = binary.BigEndian.AppendUint32(raw, wire.HeaderSize)
	raw = append(raw, make([]byte, wire.HeaderSize)...)
	if _, _, e := unpackControl(raw); e == nil {
		t.Fatal("unbounded records")
	}
}
func TestServerRejectsHistoricalHelloAgainstFreshServerChallenge(t *testing.T) {
	ctx := context.Background()
	m := &memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}
	cfg, _ := servercfg.New("test")
	a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example", Port: 993, TLS: "tls", Username: "u", Password: "p"}}
	u, err := cfg.AddUser("test")
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Ensure(ctx, u.Up); err != nil {
		t.Fatal(err)
	}
	if err = m.Ensure(ctx, u.Down); err != nil {
		t.Fatal(err)
	}
	logical, generation, err := freshAttachment()
	if err != nil {
		t.Fatal(err)
	}
	clientNonce, _ := randomID()
	serverNonce, _ := randomID()
	binding := attachedBinding(cfg.Server, u, a.ID, logical, generation, 1, transportCapabilities, clientNonce, serverNonce)
	binding.Session[0] = 1
	binding.Handshake[0] = 1
	key, _ := hex.DecodeString(u.Key)
	hs, err := secure.NewHandshake(binding, key, true)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := hs.Write(nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(hello{Version: transportWireVersion, Capabilities: transportCapabilities, LogicalSession: logical, Generation: generation, CarrierEpoch: 1, ClientNonce: clientNonce, ServerNonce: serverNonce, Binding: binding, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := mailbox.Encode(append([]byte(transportHelloMagic), encoded...))
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Append(ctx, u.Up, raw); err != nil {
		t.Fatal(err)
	}

	bootCtx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()
	if lane, err := newLane(bootCtx, cfg.Server, u, a, t.TempDir(), true, m); err == nil {
		lane.close()
		t.Fatal("historical hello authenticated without a fresh post-boot probe")
	} else if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected historical-hello result: %v", err)
	}
	m.mu.Lock()
	down := m.next[u.Down]
	rows := make([][]byte, 0, len(m.rows[u.Down]))
	for _, raw := range m.rows[u.Down] {
		rows = append(rows, append([]byte(nil), raw...))
	}
	m.mu.Unlock()
	if down == 0 || len(rows) == 0 {
		t.Fatal("server did not publish a fresh server-first challenge")
	}
	for _, raw := range rows {
		body, decodeErr := mailbox.Decode(raw)
		if decodeErr != nil || !strings.HasPrefix(string(body), transportChallengeMagic) {
			t.Fatalf("unexpected pre-auth server message: %q err=%v", body, decodeErr)
		}
	}
}

func TestControlBurstDoesNotExhaustJournal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	m := &memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}
	cfg, _ := servercfg.New("test")
	a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example", Port: 993, TLS: "tls", Username: "u", Password: "p"}}
	u, e := cfg.AddUser("test")
	if e != nil {
		t.Fatal(e)
	}
	// Leave a valid abandoned hello before the fresh handshake. Reading it
	// after accepting the newest UID must not supersede the live session.
	_ = m.Ensure(ctx, u.Up)
	_ = m.Ensure(ctx, u.Down)
	oldLogical, oldGeneration, e := freshAttachment()
	if e != nil {
		t.Fatal(e)
	}
	oldClient, _ := randomID()
	oldServer, _ := randomID()
	old := attachedBinding(cfg.Server, u, a.ID, oldLogical, oldGeneration, 1, transportCapabilities, oldClient, oldServer)
	old.Session[0] = 1
	old.Handshake[0] = 1
	key, _ := hex.DecodeString(u.Key)
	hs, e := secure.NewHandshake(old, key, true)
	if e != nil {
		t.Fatal(e)
	}
	payload, e := hs.Write(nil)
	if e != nil {
		t.Fatal(e)
	}
	encoded, _ := json.Marshal(hello{Version: transportWireVersion, Capabilities: transportCapabilities, LogicalSession: oldLogical, Generation: oldGeneration, CarrierEpoch: 1, ClientNonce: oldClient, ServerNonce: oldServer, Binding: old, Payload: payload})
	raw, _ := mailbox.Encode(append([]byte(transportHelloMagic), encoded...))
	if e = m.Append(ctx, u.Up, raw); e != nil {
		t.Fatal(e)
	}
	failures := make(chan error, 2)
	serverDir := t.TempDir()
	go func() {
		l, e := newLane(ctx, cfg.Server, u, a, serverDir, true, m)
		if e == nil {
			e = l.run()
		}
		failures <- e
	}()
	client, e := newLane(ctx, cfg.Server, u, a, t.TempDir(), false, m)
	if e != nil {
		t.Fatal(e)
	}
	go func() { failures <- client.run() }()
	wait := func(done func() bool) {
		t.Helper()
		for !done() {
			select {
			case e := <-failures:
				t.Fatal(e)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	wait(func() bool { return client.ready.Load() })

	// Field regression from gate11: a lane could be receiving authenticated
	// control traffic continuously yet hit TRANSPORT_TIMEOUT because acceptRecord
	// refreshed lastRX but not lastProgress. Force the progress clock stale, then
	// prove that a fresh PONG makes the lane healthy again without requiring DATA.
	// Keep it below the separate 25-second transport watchdog so this regression
	// tests progress refresh rather than racing the live lane supervisor.
	t.Run("authenticated_control_refreshes_transport_progress", func(t *testing.T) {
		client.lastProgress.Store(time.Now().Add(-openHealthMaxSilence - time.Second).UnixNano())
		if client.openHealthy(time.Now()) {
			t.Fatal("stale progress setup did not make lane unhealthy")
		}
		initial := client.pongs.Load()
		if e = client.controlSend(wire.Echo{Token: 999}); e != nil {
			t.Fatal(e)
		}
		wait(func() bool { return client.pongs.Load() > initial })
		if age := client.remoteProgressAge(time.Now()); age > time.Second {
			t.Fatalf("authenticated PONG did not refresh transport progress: age=%v", age)
		}
		if !client.openHealthy(time.Now()) {
			t.Fatal("fresh authenticated control traffic must restore lane health")
		}
	})

	initial := client.pongs.Load()
	for i := 0; i < 240; i++ {
		if e = client.controlSend(wire.Echo{Token: uint64(1000 + i)}); e != nil {
			t.Fatal(e)
		}
	}
	wait(func() bool { return client.pongs.Load() >= initial+240 })
	held, e := client.control.Held(a.ID, u.ID)
	if e != nil || held.Messages >= 128 {
		t.Fatal(held, e)
	}
	t.Logf("240 request/reply controls completed; client journal holds %d messages", held.Messages)
	cancel()
	for i := 0; i < 2; i++ {
		select {
		case <-failures:
		case <-time.After(5 * time.Second):
			t.Fatal("shutdown did not join")
		}
	}
}

func checkInitialRuntimeWindowIsLatencyCritical(t *testing.T) {
	initial := wire.Frame{Type: wire.Window, Stream: 7, Offset: runtimeStreamWindow}
	if !latencyControl(initial) {
		t.Fatal("initial stream WINDOW must use the latency-critical control path")
	}
	sliding := initial
	sliding.Offset++
	if readerResponseWindow(sliding) != 0 {
		t.Fatal("sliding WINDOW must not create reciprocal reader polling")
	}
	if latencyControl(sliding) {
		t.Fatal("sliding WINDOW must preserve the separate critical reserve")
	}
}

func checkDiagnosticErrorsDoNotExposeProviderText(t *testing.T) {
	d := &diagnostic{log: func(s string) {
		if strings.Contains(s, "secret.example") {
			t.Fatal("provider text leaked")
		}
	}}
	d.reconnect(&providerError{}, time.Second)
}

type providerError struct{}

func (*providerError) Error() string { return "secret.example password=private" }

func checkMailReceiptUsesBoundedOldestWindow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := &lane{
		ctx:              ctx,
		known:            map[uint64]mailbox.Message{},
		receiptQueue:     make(chan queuedControl, 1),
		controlQueue:     make(chan queuedControl, 1),
		urgentQueue:      make(chan queuedControl, 1),
		maintenanceQueue: make(chan queuedControl, 1),
	}
	for i := uint64(1); i <= 700; i++ {
		l.known[i] = mailbox.Message{Validity: 1, UID: uint32(i)}
	}
	l.ackDirty = true
	if err := l.sendReceipt(); err != nil {
		t.Fatal(err)
	}
	select {
	case queued := <-l.receiptQueue:
		decoded, err := wire.DecodeControl(queued.Frame)
		if err != nil {
			t.Fatal(err)
		}
		receipt, ok := decoded.(wire.MailReceived)
		if !ok {
			t.Fatalf("unexpected receipt type %T", decoded)
		}
		if len(receipt.Counters) != receiptFrameCounters {
			t.Fatalf("receipt counters=%d want=%d", len(receipt.Counters), receiptFrameCounters)
		}
		if receipt.Counters[0] != 1 || receipt.Counters[len(receipt.Counters)-1] != receiptFrameCounters {
			t.Fatalf("receipt window=%d..%d", receipt.Counters[0], receipt.Counters[len(receipt.Counters)-1])
		}
	default:
		t.Fatal("receipt was not queued")
	}
}

// Resource-independent checks form one scenario; lifecycle and concurrency
// checks in this file remain independently scheduled.
func TestScenarioControl(t *testing.T) {
	t.Log("check=checkControlEnvelopeBounds")
	checkControlEnvelopeBounds(t)
	t.Log("check=checkInitialRuntimeWindowIsLatencyCritical")
	checkInitialRuntimeWindowIsLatencyCritical(t)
	t.Log("check=checkDiagnosticErrorsDoNotExposeProviderText")
	checkDiagnosticErrorsDoNotExposeProviderText(t)
	t.Log("check=checkMailReceiptUsesBoundedOldestWindow")
	checkMailReceiptUsesBoundedOldestWindow(t)
}
