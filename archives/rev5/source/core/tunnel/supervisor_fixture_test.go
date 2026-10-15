package tunnel

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
)

// This fixture invokes the actual client/server supervisors. Only provider dial
// and private test TCP targets are injected; lane construction/retry/epochs,
// shared DATA workers and startup recovery are exactly the production path.
type productionSupervisorFixture struct {
	ctx                  context.Context
	cancel               context.CancelFunc
	client               *Client
	network              *providerTestNetwork
	serverDone           chan error
	dialAttempts         []atomic.Int64
	dialDenied           []atomic.Int64
	mu                   sync.Mutex
	events               []string
	targetDials          atomic.Int64
	baselineDial         func(context.Context) (*mailbox.Client, error)
	listener             net.Listener
	serverLanes          map[string]*lane
	carrierExits         *carrierExitMonitor
	serverDir, clientDir string
	serverLogs           atomic.Int64
}

func newProductionSupervisorFixture(t *testing.T) *productionSupervisorFixture {
	return newProductionSupervisorFixtureAccounts(t, 2)
}
func newProductionSupervisorFixtureAccounts(t *testing.T, accounts int) *productionSupervisorFixture {
	return newProductionSupervisorFixtureState(t, accounts, nil)
}

// State setup runs before either supervisor opens its databases. Field replay
// tests use installed legacy journals and wallets instead of a fresh empty pair.
func newProductionSupervisorFixtureState(t *testing.T, accounts int, setup func(string, string, servercfg.Config, servercfg.User)) *productionSupervisorFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	f := &productionSupervisorFixture{serverLanes: map[string]*lane{}, ctx: ctx, cancel: cancel, network: newProviderTestNetwork(0), serverDone: make(chan error, 1)}
	serverCommitDelay := time.Duration(loadEnvInt(t, "ITOM_LOAD_SERVER_COMMIT_US", 0, 0, 100000)) * time.Microsecond
	clientCommitDelay := time.Duration(loadEnvInt(t, "ITOM_LOAD_CLIENT_COMMIT_US", 0, 0, 100000)) * time.Microsecond
	validateTestCommitDelay(t, serverCommitDelay, clientCommitDelay)
	t.Logf("commit_delay_model server_us=%d client_control_us=%d actual_fsync_retained=true", serverCommitDelay.Microseconds(), clientCommitDelay.Microseconds())
	f.dialAttempts = make([]atomic.Int64, accounts)
	f.dialDenied = make([]atomic.Int64, accounts)
	f.carrierExits = newCarrierExitMonitor(ctx)
	if os.Getenv("ITOM_FIELD_PROVIDER_MODEL") == "1" {
		if os.Getenv("ITOM_FIELD_LATENCY_PROFILE") == "rev1-wrapper" {
			f.network.fieldLatency = newRev1WrapperStress(t)
		} else if os.Getenv("ITOM_FIELD_LATENCY_PROFILE") == "recorded" {
			f.network.fieldLatency = newFieldLatencyReplay(t)
		} else {
			f.network.fieldLatency = newProviderServiceProfile(t)
		}
	}
	f.network.visibility.delay = time.Duration(loadEnvInt(t, "ITOM_LOAD_VISIBILITY_DELAY_MS", 0, 0, 5000)) * time.Millisecond
	f.network.mobileEnabled = os.Getenv("ITOM_LOAD_MOBILE") == "1"
	if f.network.mobileEnabled {
		profile := mobileLinkProfile{RTT: time.Duration(loadEnvInt(t, "ITOM_LOAD_MOBILE_RTT_MS", 200, 0, 1000)) * time.Millisecond, Mbps: loadEnvFloat(t, "ITOM_LOAD_MOBILE_MBPS", 600), SharedMedium: os.Getenv("ITOM_LOAD_MOBILE_SHARED_MEDIUM") == "1", LossPercent: loadEnvFloat(t, "ITOM_LOAD_MOBILE_LOSS_PERCENT", 0), Seed: int64(loadEnvInt(t, "ITOM_LOAD_FAULT_SEED", 1, 0, 1<<30))}
		if !f.network.mobile.set(profile) {
			t.Fatal("invalid mobile profile before bootstrap")
		}
	}
	f.network.sessionLimit = loadEnvInt(t, "ITOM_LOAD_PROVIDER_CONNECTION_LIMIT", 0, 0, 32)
	cfg, e := servercfg.New("supervisor-test")
	if e != nil {
		t.Fatal(e)
	}
	u, e := cfg.AddUser("fixture")
	if e != nil {
		t.Fatal(e)
	}
	cfg.Transport = servercfg.Transport{Layout: os.Getenv("ITOM_LOAD_LAYOUT"), DataShards: loadEnvInt(t, "ITOM_LOAD_DATA_SHARDS", 1, 1, 4), AccountSessions: loadEnvInt(t, "ITOM_LOAD_ACCOUNT_SESSIONS", 10, 2, 128), EndpointConnections: loadEnvInt(t, "ITOM_LOAD_ENDPOINT_CONNECTIONS", 128, 2, 4096), LoginParallelism: loadEnvInt(t, "ITOM_LOAD_LOGIN_PARALLELISM", 4, 1, 32)}
	providers := map[string]func(context.Context) (*mailbox.Client, error){}
	history := loadEnvInt(t, "ITOM_LOAD_UID_HISTORY", 0, 0, 8192)
	// Historical allocation is outside the observed application's network turn.
	// Seed through real IMAP commands, with command latency enabled only once
	// both endpoints start. Seed sockets are closed before that transition.
	f.network.historySetup.Store(history > 0)
	for i := 0; i < accounts; i++ {
		id := fmt.Sprintf("a%02d", i+1)
		c, s := tlsMailPairWithNetwork(t, ctx, []string{u.Up, u.Down}, f.network, id)
		seed := c.(*laneMail)
		if history > 0 {
			for _, base := range []string{u.Up, u.Down} {
				seedPhysicalUIDHistory(t, ctx, seed.Client, base+"-data-00", history)
			}
		}
		providers[id] = seed.dial
		cfg.Accounts = append(cfg.Accounts, servercfg.Account{ID: id, Mail: mailbox.Config{Host: "mail.example", Port: 993, TLS: "tls", Username: id, Password: "test-only"}})
		c.(*laneMail).Close()
		s.(*laneMail).Close()
	}
	f.network.historySetup.Store(false)
	dial := func(ctx context.Context, c mailbox.Config, folders []string) (*mailbox.Client, error) {
		i := 0
		fmt.Sscanf(c.Username, "a%02d", &i)
		i--
		if i < 0 || i >= accounts {
			return nil, fmt.Errorf("TEST_ACCOUNT_SCOPE")
		}
		f.dialAttempts[i].Add(1)
		if !f.network.accountAvailable(c.Username) {
			f.dialDenied[i].Add(1)
			return nil, fmt.Errorf("TEST_PROVIDER_ACCOUNT_DOWN")
		}
		return providers[c.Username](ctx)
	}
	f.baselineDial = func(ctx context.Context) (*mailbox.Client, error) {
		c, err := providers["a01"](ctx)
		if err == nil && (f.network.mobileEnabled || f.network.fieldLatency != nil && f.network.fieldLatency.endpointScoped) && !f.network.markMobileClient(c.LocalAddr().String(), c.RemoteAddr().String()) {
			c.Close()
			return nil, fmt.Errorf("MOBILE_BASELINE_SCOPE")
		}
		return c, err
	}
	clientDial := func(ctx context.Context, cfg mailbox.Config, folders []string) (*mailbox.Client, error) {
		c, err := dial(ctx, cfg, folders)
		if err == nil && (f.network.mobileEnabled || f.network.fieldLatency != nil && f.network.fieldLatency.endpointScoped) && !f.network.markMobileClient(c.LocalAddr().String(), c.RemoteAddr().String()) {
			c.Close()
			return nil, fmt.Errorf("MOBILE_CLIENT_SCOPE")
		}
		return c, err
	}
	f.listener, e = net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	var targetMu sync.Mutex
	var targetConns []net.Conn
	go func() {
		for {
			c, e := f.listener.Accept()
			if e != nil {
				return
			}
			f.targetDials.Add(1)
			targetMu.Lock()
			targetConns = append(targetConns, c)
			targetMu.Unlock()
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	record := func(s string) {
		f.mu.Lock()
		f.events = append(f.events, s)
		if len(f.events) > 128 {
			f.events = append([]string(nil), f.events[len(f.events)-128:]...)
		}
		f.mu.Unlock()
	}
	serverDir := filepath.Join(t.TempDir(), "server")
	clientDir := filepath.Join(t.TempDir(), "client")
	if setup != nil {
		setup(serverDir, clientDir, cfg, u)
	}
	f.serverDir, f.clientDir = serverDir, clientDir
	if os.Getenv("ITOM_LOAD_SERVER_DIAGNOSTICS_OFF") == "1" {
		cfg.DiagnosticsMode = "off"
	}
	go func() {
		f.serverDone <- serveWithOptions(ctx, cfg, serverDir, func(s string) { f.serverLogs.Add(1); record(s) }, serverOptions{dial: dial, readNotice: noticeSource(serverDir), allowPrivate: true, onCarrierExit: f.carrierExits.observe, diagnostics: os.Getenv("ITOM_LOAD_DIAGNOSTICS") == "1", onLane: func(l *lane) {
			applyTestCommitDelay(l, serverCommitDelay)
			f.mu.Lock()
			f.serverLanes[l.account.ID] = l
			f.mu.Unlock()
			record(fmt.Sprintf("event=carrier_authenticated account=%s session=%x generation=%d epoch=%d", l.account.ID, l.logicalSession, l.generation, l.carrierEpoch))
		}})
	}()
	profile := servercfg.Profile{Schema: 1, Server: cfg.Server, Provider: cfg.Provider, User: u, Accounts: cfg.Accounts, Transport: cfg.TransportForUsers()}
	f.client, e = StartClientWithOptions(ctx, profile, clientDir, clientDial, ClientOptions{Diagnostics: os.Getenv("ITOM_LOAD_DIAGNOSTICS") == "1", onCarrierExit: f.carrierExits.observe})
	if e != nil {
		cancel()
		t.Fatal(e)
	}
	t.Cleanup(func() {
		cancel()
		f.listener.Close()
		targetMu.Lock()
		for _, c := range targetConns {
			c.Close()
		}
		targetMu.Unlock()
		joined := make(chan struct{})
		go func() { f.client.Close(); <-f.serverDone; close(joined) }()
		select {
		case <-joined:
		case <-time.After(10 * time.Second):
			t.Error("supervisor cancellation deadline exceeded")
		}
		for _, detail := range f.carrierExits.failureDetails() {
			t.Logf("supervisor_unexpected_exit %v", detail)
		}
		if count := f.carrierExits.unexpected.Load(); count != 0 {
			t.Errorf("unexpected carrier exits during fixture lifetime: %d", count)
		}
		f.mu.Lock()
		events := append([]string(nil), f.events...)
		f.mu.Unlock()
		for _, event := range events {
			t.Logf("supervisor_timeline %s", event)
		}
		if directory := os.Getenv("ITOM_LOAD_TRACE_DIR"); directory != "" {
			for side, root := range map[string]string{"client": clientDir, "server": serverDir} {
				files, _ := filepath.Glob(filepath.Join(root, "transport-trace*"))
				for _, file := range files {
					raw, err := os.ReadFile(file)
					if err != nil {
						t.Error(err)
						continue
					}
					dest := filepath.Join(directory, side)
					if err = os.MkdirAll(dest, 0700); err != nil {
						t.Error(err)
						continue
					}
					if err = os.WriteFile(filepath.Join(dest, filepath.Base(file)), raw, 0600); err != nil {
						t.Error(err)
					}
				}
			}
		}
		var attempts, denied int64
		for i := range f.dialAttempts {
			attempts += f.dialAttempts[i].Load()
			denied += f.dialDenied[i].Load()
		}
		t.Logf("supervisor_summary accounts=%d dial_attempts=%d dial_denied=%d target_tcp_dials=%d provider=%+v", len(f.dialAttempts), attempts, denied, f.targetDials.Load(), f.network.snapshot())
	})
	for i := 1; i <= accounts; i++ {
		l := f.waitLane(t, fmt.Sprintf("a%02d", i), 0)
		applyTestCommitDelay(l, clientCommitDelay)
	}
	return f
}
func (f *productionSupervisorFixture) waitLane(t *testing.T, id string, after uint64) *lane {
	t.Helper()
	timer := time.NewTimer(35 * time.Second)
	defer timer.Stop()
	for {
		f.client.mu.Lock()
		l := f.client.lanes[id]
		ready := l != nil && l.ctx.Err() == nil && l.streamCarrierReady() && l.carrierEpoch > after
		f.client.mu.Unlock()
		if ready {
			// Restore both endpoints before rotating to the other half.
			f.mu.Lock()
			server := f.serverLanes[id]
			ready = server != nil && server.ctx.Err() == nil && server.streamCarrierReady()
			f.mu.Unlock()
			if ready {
				return l
			}
		}
		select {
		case <-timer.C:
			t.Fatalf("carrier %s failed to rejoin after epoch %d", id, after)
		case <-f.ctx.Done():
			t.Fatal(f.ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}
func (f *productionSupervisorFixture) openOn(t *testing.T, id string) *pipe {
	t.Helper()
	l := f.waitLane(t, id, 0)
	_, port, _ := net.SplitHostPort(f.listener.Addr().String())
	var n int
	fmt.Sscanf(port, "%d", &n)
	p, e := l.open(f.ctx, wire.Connect{Host: "127.0.0.1", Port: uint16(n)})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { p.Close() })
	return p
}
func supervisorEcho(p net.Conn, index int) error {
	_ = p.SetDeadline(time.Now().Add(2 * time.Second))
	data := []byte(fmt.Sprintf("same-socket-sequence/%d", index))
	got := make([]byte, len(data))
	if e := productionWriteFull(p, data); e != nil {
		return e
	}
	if _, e := io.ReadFull(p, got); e != nil {
		return e
	}
	if !bytes.Equal(data, got) {
		return fmt.Errorf("socket content/order mismatch")
	}
	return nil
}
func TestLoadedApplicationSupervisorNonOwnerRejoin(t *testing.T) {
	f := newProductionSupervisorFixture(t)
	p := f.openOn(t, "a01")
	old := f.waitLane(t, "a02", 0)
	logical, generation := f.client.logicalSession, f.client.generation
	if e := supervisorEcho(p, 0); e != nil {
		t.Fatal(e)
	}
	dropped := f.outageAccount("a02", 1500*time.Millisecond)
	t.Logf("supervisor_fault account=a02 owner=a01 dropped=%d duration_ms=1500", dropped)
	for i := 1; i <= 10; i++ {
		if e := supervisorEcho(p, i); e != nil {
			t.Fatalf("healthy owner socket failed during sibling outage: %v", e)
		}
		if e := pause(f.ctx, 100*time.Millisecond); e != nil {
			t.Fatal(e)
		}
	}
	returned := f.waitLane(t, "a02", old.carrierEpoch)
	if returned.logicalSession != logical || returned.generation != generation || f.targetDials.Load() != 1 || f.dialDenied[1].Load() == 0 {
		t.Fatal("session/dial/retry invariant failed")
	}
	if e := supervisorEcho(p, 11); e != nil {
		t.Fatal(e)
	}
	// Repeat the failure to exercise the recovered carrier, not only first setup.
	f.outageAccount("a02", 500*time.Millisecond)
	f.waitLane(t, "a02", returned.carrierEpoch)
	if e := supervisorEcho(p, 12); e != nil || f.targetDials.Load() != 1 {
		t.Fatal("repeated rejoin", e)
	}
}

// Repeated physical account loss must retain the same logical actor and socket.
// The full build always checks physical owner survival, including recovery.
func TestLoadedApplicationSupervisorOwnerSurvival(t *testing.T) {
	f := newProductionSupervisorFixture(t)
	p := f.openOn(t, "a01")
	if e := supervisorEcho(p, 0); e != nil {
		t.Fatal(e)
	}
	owner := f.client.stripe.accountOwner("a01")
	logical, generation := f.client.logicalSession, f.client.generation
	old := f.waitLane(t, "a01", 0)
	for round, duration := range []time.Duration{2 * time.Second, 5 * time.Second} {
		f.outageAccount("a01", duration)
		t.Logf("supervisor_owner_fault round=%d duration_ms=%d epoch=%d", round+1, duration.Milliseconds(), old.carrierEpoch)
		for i := 1; i <= 8; i++ {
			if e := supervisorEcho(p, round*100+i); e != nil {
				t.Fatalf("same owner socket did not survive: %v", e)
			}
			if e := pause(f.ctx, 100*time.Millisecond); e != nil {
				t.Fatal(e)
			}
		}
		old = f.waitLane(t, "a01", old.carrierEpoch)
		if old.logicalSession != logical || old.generation != generation || f.client.stripe.accountOwner("a01") != owner {
			t.Fatal("rejoin replaced logical owner/session budget")
		}
		if f.client.Stats().TCP != 1 {
			t.Fatal("rejoined carrier hid or duplicated active socket stats")
		}
		if e := supervisorEcho(p, round*100+20); e != nil {
			t.Fatal("echo after rejoin", e)
		}
	}
	if f.targetDials.Load() != 1 {
		t.Fatal("target dial duplicated")
	}

}

func TestLoadedApplicationSupervisorNoFault(t *testing.T) {
	f := newProductionSupervisorFixture(t)
	p := f.openOn(t, "a01")
	for i := 0; i < 5; i++ {
		if e := supervisorEcho(p, i); e != nil {
			t.Fatal(e)
		}
	}
	// A server download reaching its soft key limit asks the client to rotate
	// this one physical carrier. Retain the existing TCP socket and owner.
	old := f.waitLane(t, "a01", 0)
	owner := f.client.stripe.accountOwner("a01")
	f.mu.Lock()
	server := f.serverLanes["a01"]
	f.mu.Unlock()
	f.carrierExits.expectCode(old, "CARRIER_REKEY")
	f.carrierExits.expectCode(server, "CARRIER_REATTACH")
	started := time.Now()
	if err := server.controlSend(wire.Echo{Token: rekeyRequestToken | old.carrierEpoch}); err != nil {
		t.Fatal(err)
	}
	for i := 10; i < 16; i++ {
		if err := supervisorEcho(p, i); err != nil {
			t.Fatal("TCP stalled while one physical account rotated", err)
		}
	}
	f.waitLane(t, "a01", old.carrierEpoch)
	// The original bounded waitLane budget includes durable inventory on both
	// hosts. Judge continuity and exact transition reasons, not a new wall-clock
	// assertion that confounds disk/OS speed with the application's RTT.
	t.Logf("planned_rekey_ms=%d same_socket=true same_owner=true", time.Since(started).Milliseconds())
	if f.client.stripe.accountOwner("a01") != owner {
		t.Fatal("planned rekey replaced logical owner")
	}
	if e := supervisorEcho(p, 6); e != nil {
		t.Fatal("same TCP socket failed after planned rekey", e)
	}
	if f.targetDials.Load() != 1 {
		t.Fatal("duplicate target dial")
	}
}

// Exempt only the exact carrier lifetimes deliberately stopped by the fixture.
// A replacement on the same account receives no exemption.
func (f *productionSupervisorFixture) outageAccount(id string, duration time.Duration) int {
	f.client.mu.Lock()
	client := f.client.lanes[id]
	f.client.mu.Unlock()
	f.mu.Lock()
	server := f.serverLanes[id]
	f.mu.Unlock()
	f.carrierExits.expect(client, server)
	return f.network.outageAccount(id, duration)
}
