package tunnel

import (
	"context"
	"os"
	"runtime"
	"sort"
	"testing"
	"time"
)

// Healthy-server reference: measured server route, configurable client route.
// Only test fixtures read these settings. No live transport policy is changed.
func configureHealthyEndpoints(t *testing.T, p *providerTestNetwork) {
	t.Helper()
	if os.Getenv("ITOM_HEALTHY_SERVER") != "1" {
		return
	}
	for _, key := range []string{"ITOM_LOAD_MOBILE", "ITOM_FIELD_PROVIDER_MODEL", "ITOM_LOAD_DROP_HELLO_REPLY", "ITOM_LOAD_SUPPRESS_IDLE_NOTIFICATIONS"} {
		if os.Getenv(key) == "1" {
			t.Fatalf("healthy reference forbids injected server/provider fault: %s", key)
		}
	}
	for _, key := range []string{"ITOM_LOAD_COMMAND_DELAY_MS", "ITOM_LOAD_VISIBILITY_DELAY_MS", "ITOM_LOAD_SERVER_COMMIT_US", "ITOM_LOAD_PROVIDER_IDLE_TIMEOUT_MS"} {
		if value := os.Getenv(key); value != "" && value != "0" {
			t.Fatalf("healthy reference forbids legacy artificial delay: %s=%s", key, value)
		}
	}
	clientRTT := time.Duration(loadEnvInt(t, "ITOM_LOAD_CLIENT_RTT_MS", 40, 0, 1000)) * time.Millisecond
	serverRTT := time.Duration(loadEnvInt(t, "ITOM_LOAD_SERVER_RTT_MS", 20, 0, 1000)) * time.Millisecond
	clientMbps := loadEnvFloat(t, "ITOM_LOAD_CLIENT_MBPS", 600)
	serverMbps := loadEnvFloat(t, "ITOM_LOAD_SERVER_MBPS", 1000)
	if clientMbps <= 0 || serverMbps <= 0 {
		t.Fatal("endpoint capacities must be positive")
	}
	p.healthyEndpoints = true
	p.mobileEnabled = true
	if !p.mobile.set(mobileLinkProfile{RTT: clientRTT, Mbps: clientMbps, UplinkMbps: loadEnvFloat(t, "ITOM_LOAD_CLIENT_UPLINK_MBPS", 0), Seed: 714}) || !p.serverPath.set(mobileLinkProfile{RTT: serverRTT, Mbps: serverMbps, Seed: 715}) {
		t.Fatal("invalid healthy endpoint path")
	}
	// Old combined cap is deliberately absent: each endpoint pays its own
	// actual TLS bytes once on its own independently shared full-duplex path.
	p.setMbps(0)
	startModelMobileTrace(t, p)
	t.Logf("healthy_reference server_rtt_ms=%d server_mbps=%.3f client_rtt_ms=%d client_mbps=%.3f provider_service_ms=%d injected_server_faults=false physical_paths=independent runtime_cpus=%d gomaxprocs=%d deployment_server_cpus=8 cpu_model=shared_test_process field_qualified=false", serverRTT.Milliseconds(), serverMbps, clientRTT.Milliseconds(), clientMbps, loadEnvInt(t, "ITOM_LOAD_PROVIDER_SERVICE_MS", 2, 0, 100), runtime.NumCPU(), runtime.GOMAXPROCS(0))
}

func (p *providerTestNetwork) setApplicationPathMbps(mbps float64) {
	if p.healthyEndpoints {
		// An absent legacy aggregate cap must not erase the explicit client route.
		if mbps <= 0 {
			return
		}
		profile := p.mobile.profile.Load()
		if profile == nil {
			panic("missing client path")
		}
		next := *profile
		next.Mbps = mbps
		if !p.mobile.set(next) {
			panic("invalid client capacity")
		}
		return
	}
	p.setMbps(mbps)
}

// A phone/network interruption must not take the server's provider path down.
func (p *providerTestNetwork) interruptClientPath(duration time.Duration) {
	p.clientDownUntil.Store(time.Now().Add(duration).UnixNano())
	p.mu.Lock()
	var clients []*providerTestConn
	for _, c := range p.active {
		if c.mobileClient.Load() {
			clients = append(clients, c)
		}
	}
	p.mu.Unlock()
	for _, c := range clients {
		_ = c.Close()
	}
}

func TestHealthyServerClientInterruptionPreservesTCP(t *testing.T) {
	t.Setenv("ITOM_HEALTHY_SERVER", "1")
	t.Setenv("ITOM_LOAD_COMMAND_DELAY_MS", "0")
	t.Setenv("ITOM_LOAD_CLIENT_RTT_MS", "40")
	f := newProductionSupervisorFixtureAccounts(t, 2)
	connection := rev4Open(t, f)
	if err := rev4Exchange(connection, 1, 4096, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	logical, generation := f.client.logicalSession, f.client.generation
	f.mu.Lock()
	var serverLane *lane
	for _, l := range f.serverLanes {
		serverLane = l
		break
	}
	f.mu.Unlock()
	if serverLane == nil {
		t.Fatal("missing healthy server carrier")
	}
	// Only the deliberately interrupted carrier lifetimes are expected.
	// Healthy-server replacement accepts only the authenticated reattach code;
	// any other server failure, or any later replacement failure, still fails.
	f.client.mu.Lock()
	for _, l := range f.client.lanes {
		f.carrierExits.expect(l)
	}
	f.client.mu.Unlock()
	f.mu.Lock()
	for _, l := range f.serverLanes {
		f.carrierExits.expectCode(l, "CARRIER_REATTACH")
	}
	f.mu.Unlock()
	f.network.interruptClientPath(time.Second)
	// Exercise an actual authenticated server/provider command while the
	// phone is offline; this is not a provider-wide outage fixture.
	if _, err := physicalMail(serverLane.mail).controlClient().Ping(f.ctx); err != nil {
		t.Fatal("phone interruption took server route down", err)
	}
	if err := rev4Exchange(connection, 2, 128<<10, 12*time.Second); err != nil {
		t.Fatal("original TCP did not recover after client interruption", err)
	}
	if f.client.logicalSession != logical || f.client.generation != generation || f.targetDials.Load() != 1 {
		t.Fatal("client recovery replaced TCP identity")
	}
}

func TestHealthyEndpointScopeAndCapacityTransitions(t *testing.T) {
	t.Setenv("ITOM_HEALTHY_SERVER", "1")
	t.Setenv("ITOM_LOAD_COMMAND_DELAY_MS", "0")
	p := newProviderTestNetwork(0)
	configureHealthyEndpoints(t, p)
	server := &providerTestConn{network: p}
	client := &providerTestConn{network: p}
	client.mobileClient.Store(true)
	a, b := &mobileRelay{owner: server}, &mobileRelay{owner: client}
	if a.pathState() != &p.serverPath || b.pathState() != &p.mobile {
		t.Fatal("endpoint paths aliased")
	}
	original := *p.serverPath.profile.Load()
	for _, rate := range []float64{10, 20, 50, 150, 600} {
		p.setApplicationPathMbps(rate)
		if *p.serverPath.profile.Load() != original || p.mobile.profile.Load().Mbps != rate {
			t.Fatal("client speedup impaired server")
		}
	}
	p.uplink.mu.Lock()
	up := p.uplink.mbps
	p.uplink.mu.Unlock()
	p.downlink.mu.Lock()
	down := p.downlink.mbps
	p.downlink.mu.Unlock()
	if up != 0 || down != 0 {
		t.Fatal("combined artificial bottleneck retained")
	}
	legacy := newProviderTestNetwork(0)
	if (&mobileRelay{owner: &providerTestConn{network: legacy}}).pathState() != nil {
		t.Fatal("legacy unmarked path gained impairment")
	}
}

func TestHealthyEndpointTLSNoopCalibration(t *testing.T) {
	t.Setenv("ITOM_HEALTHY_SERVER", "1")
	t.Setenv("ITOM_LOAD_COMMAND_DELAY_MS", "0")
	t.Setenv("ITOM_LOAD_CLIENT_RTT_MS", "100")
	t.Setenv("ITOM_LOAD_SERVER_RTT_MS", "20")
	p := newProviderTestNetwork(0)
	configureHealthyEndpoints(t, p)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, s := tlsMailPairWithNetwork(t, ctx, []string{"up", "down"}, p, "calibration")
	phone, server := physicalMail(c).Client, physicalMail(s).Client
	defer phone.Close()
	defer server.Close()
	if !p.markMobileClient(phone.LocalAddr().String(), phone.RemoteAddr().String()) {
		t.Fatal("client scope")
	}
	measure := func(name string, fn func(context.Context) (time.Duration, error)) time.Duration {
		samples := make([]time.Duration, 5)
		for i := range samples {
			rtt, err := fn(ctx)
			if err != nil {
				t.Fatal(name, err)
			}
			samples[i] = rtt
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		t.Logf("endpoint_calibration endpoint=%s method=authenticated_TLS_NOOP median_ms=%.3f", name, float64(samples[2])/float64(time.Millisecond))
		return samples[2]
	}
	srtt := measure("server", server.Ping)
	crtt := measure("client", phone.Ping)
	if !raceInstrumented && (srtt < 18*time.Millisecond || srtt > 100*time.Millisecond || crtt < 95*time.Millisecond || crtt > 220*time.Millisecond || crtt-srtt < 40*time.Millisecond) {
		t.Fatal("uncalibrated endpoint propagation", srtt, crtt)
	}
}

func TestHealthyAbsentAggregateCapPreservesConfiguredPaths(t *testing.T) {
	p := newProviderTestNetwork(0)
	p.healthyEndpoints = true
	if !p.mobile.set(mobileLinkProfile{RTT: 40 * time.Millisecond, Mbps: 500}) || !p.serverPath.set(mobileLinkProfile{RTT: 2 * time.Millisecond, Mbps: 1000}) {
		t.Fatal("profile")
	}
	p.setApplicationPathMbps(0)
	if p.mobile.profile.Load().Mbps != 500 {
		t.Fatal("absent legacy cap erased phone capacity")
	}
	p.setApplicationPathMbps(20)
	if p.mobile.profile.Load().Mbps != 20 || p.serverPath.profile.Load().Mbps != 1000 {
		t.Fatal("client ramp changed wrong route")
	}
}
