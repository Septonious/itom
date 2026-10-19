package tunnel

import (
	"io"
	"math/rand"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// providerTestNetwork wraps the TCP side below TLS, so caps and failures apply
// to the actual IMAP/TLS byte stream rather than to logical DATA counters. One
// instance may be shared by all account listeners to model a provider-wide
// symmetric access link.
type providerTestNetwork struct {
	controlAppendDisconnect   atomic.Bool
	dropHelloReply            atomic.Bool
	suppressIdleNotifications bool
	idleTimeout               time.Duration
	idleExpired               atomic.Uint64
	historySetup              atomic.Bool
	fieldLatency              *fieldLatencyReplay
	now                       func() time.Time
	debugWriter               io.Writer // optional test-only plaintext instrumentation; never emitted
	visibility                providerVisibility
	mobileEnabled             bool
	healthyEndpoints          bool
	clientDownUntil           atomic.Int64
	serverPath                mobileLinkState
	mobile                    mobileLinkState
	mu                        sync.Mutex
	nextID                    uint64
	active                    map[uint64]*providerTestConn
	downTill                  map[string]time.Time
	uplink                    providerRateLane
	downlink                  providerRateLane
	sessionLimit              int
	sessions                  map[string]int
	sessionPeak               map[string]int
	sessionDenied             int

	accepted  atomic.Uint64
	dropped   atomic.Uint64
	upBytes   atomic.Uint64
	downBytes atomic.Uint64
}

type providerRateLane struct {
	mu   sync.Mutex
	mbps float64
	next time.Time
}

func (r *providerRateLane) setMbps(mbps float64) {
	r.mu.Lock()
	r.mbps = mbps
	// Do not carry a queue accumulated under an older cap into the new profile.
	r.next = time.Now()
	r.mu.Unlock()
}

func (r *providerRateLane) wait(n int) { _ = r.waitUntil(n, nil) }
func (r *providerRateLane) waitUntil(n int, done <-chan struct{}) bool {
	return r.waitUntilAt(n, time.Now(), done)
}

// Preserve the byte clock from network arrival. A late timer wake must not
// reset serialization for every TLS record and impose an artificial rate cap.
func (r *providerRateLane) waitUntilAt(n int, arrival time.Time, done <-chan struct{}) bool {
	if n <= 0 {
		return true
	}
	r.mu.Lock()
	mbps := r.mbps
	if mbps <= 0 {
		r.mu.Unlock()
		return true
	}
	now := time.Now()
	start := arrival
	if r.next.After(start) {
		start = r.next
	}
	service := time.Duration(float64(n*8) / (mbps * 1e6) * float64(time.Second))
	if service < 0 {
		service = 0
	}
	r.next = start.Add(service)
	wait := start.Sub(now)
	r.mu.Unlock()
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-done:
			return false
		}
	}
	return true
}

func newProviderTestNetwork(mbps float64) *providerTestNetwork {
	p := &providerTestNetwork{active: make(map[uint64]*providerTestConn), downTill: make(map[string]time.Time), sessions: map[string]int{}, sessionPeak: map[string]int{}}
	p.setMbps(mbps)
	return p
}

func (p *providerTestNetwork) setMbps(mbps float64) {
	if p == nil {
		return
	}
	p.uplink.setMbps(mbps)
	p.downlink.setMbps(mbps)
}

func (p *providerTestNetwork) accountAvailable(account string) bool {
	if p == nil {
		return true
	}
	p.mu.Lock()
	until := p.downTill[account]
	if !until.IsZero() && !p.clock().Before(until) {
		delete(p.downTill, account)
		until = time.Time{}
	}
	p.mu.Unlock()
	return until.IsZero()
}

func (p *providerTestNetwork) wrap(account string, c net.Conn) net.Conn {
	if p == nil {
		return c
	}
	p.mu.Lock()
	p.nextID++
	pc := &providerTestConn{Conn: c, network: p, account: account, id: p.nextID, done: make(chan struct{})}
	pc.originalPeer = c.RemoteAddr().String()
	pc.originalLocal = c.LocalAddr().String()
	p.active[pc.id] = pc
	p.accepted.Add(1)
	p.mu.Unlock()
	if p.mobileEnabled || p.healthyEndpoints {
		pc.Conn = newMobileRelay(c, pc)
	}
	if p.idleTimeout > 0 {
		pc.lastRequest.Store(time.Now().UnixNano())
		go func() {
			tick := time.NewTicker(time.Second)
			defer tick.Stop()
			for {
				select {
				case <-pc.done:
					return
				case now := <-tick.C:
					if now.Sub(time.Unix(0, pc.lastRequest.Load())) >= p.idleTimeout {
						p.idleExpired.Add(1)
						pc.Close()
						return
					}
				}
			}
		}()
	}
	return pc
}

func (p *providerTestNetwork) unregister(id uint64) {
	if p == nil || id == 0 {
		return
	}
	p.mu.Lock()
	delete(p.active, id)
	p.mu.Unlock()
}

func (p *providerTestNetwork) closeIDs(ids []uint64) int {
	if p == nil || len(ids) == 0 {
		return 0
	}
	var conns []*providerTestConn
	p.mu.Lock()
	for _, id := range ids {
		if c := p.active[id]; c != nil {
			delete(p.active, id)
			conns = append(conns, c)
		}
	}
	p.mu.Unlock()
	for _, c := range conns {
		if c.closed.CompareAndSwap(false, true) {
			if c.done != nil {
				close(c.done)
			}
			_ = c.Conn.Close()
			p.dropped.Add(1)
		}
	}
	return len(conns)
}

func (p *providerTestNetwork) dropRandom(rng *rand.Rand) (account string, ok bool) {
	if p == nil || rng == nil {
		return "", false
	}
	p.mu.Lock()
	ids := make([]uint64, 0, len(p.active))
	for id := range p.active {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(ids) == 0 {
		p.mu.Unlock()
		return "", false
	}
	id := ids[rng.Intn(len(ids))]
	account = p.active[id].account
	p.mu.Unlock()
	return account, p.closeIDs([]uint64{id}) == 1
}

func (p *providerTestNetwork) outageAccount(account string, duration time.Duration) int {
	if p == nil || account == "" || duration <= 0 {
		return 0
	}
	until := p.clock().Add(duration)
	var ids []uint64
	p.mu.Lock()
	p.downTill[account] = until
	for id, c := range p.active {
		if c.account == account {
			ids = append(ids, id)
		}
	}
	p.mu.Unlock()
	return p.closeIDs(ids)
}

func (p *providerTestNetwork) snapshot() providerNetworkSnapshot {
	if p == nil {
		return providerNetworkSnapshot{}
	}
	p.mu.Lock()
	active := len(p.active)
	down := 0
	now := time.Now()
	for account, until := range p.downTill {
		if now.Before(until) {
			down++
		} else {
			delete(p.downTill, account)
		}
	}
	p.mu.Unlock()
	p.uplink.mu.Lock()
	upMbps := p.uplink.mbps
	p.uplink.mu.Unlock()
	snapshot := providerNetworkSnapshot{Active: active, DownAccounts: down, Accepted: p.accepted.Load(), Dropped: p.dropped.Load(), UplinkBytes: p.upBytes.Load(), DownlinkBytes: p.downBytes.Load(), Mbps: upMbps, HealthyEndpoints: p.healthyEndpoints}
	if p.healthyEndpoints {
		if profile := p.mobile.profile.Load(); profile != nil {
			snapshot.ClientMbps = profile.Mbps
			snapshot.ClientRTTMS = profile.RTT.Milliseconds()
		}
		if profile := p.serverPath.profile.Load(); profile != nil {
			snapshot.ServerMbps = profile.Mbps
			snapshot.ServerRTTMS = profile.RTT.Milliseconds()
		}
	}
	return snapshot
}

type providerNetworkSnapshot struct {
	HealthyEndpoints           bool
	ClientMbps, ServerMbps     float64
	ClientRTTMS, ServerRTTMS   int64
	Active, DownAccounts       int
	Accepted, Dropped          uint64
	UplinkBytes, DownlinkBytes uint64
	Mbps                       float64
}

type providerTestConn struct {
	lastRequest       atomic.Int64
	mobileClient      atomic.Bool
	pacedDownstream   atomic.Bool
	perSocketDownlink providerRateLane
	originalLocal     string
	originalPeer      string
	net.Conn
	network         *providerTestNetwork
	account         string
	id              uint64
	writeStallUntil atomic.Int64 // test-only live-socket response stall
	closed          atomic.Bool
	done            chan struct{}
}

func (c *providerTestConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.lastRequest.Store(time.Now().UnixNano())
	}
	if n > 0 && c.network != nil {
		if !c.network.uplink.waitUntil(n, c.done) {
			return 0, net.ErrClosed
		}
		c.network.upBytes.Add(uint64(n))
	}
	return n, err
}

func (c *providerTestConn) Write(p []byte) (int, error) {
	if c.pacedDownstream.Load() {
		total := 0
		for len(p) > 0 {
			chunk := p[:min(len(p), 1448)]
			if !c.perSocketDownlink.waitUntil(len(chunk), c.done) {
				return total, net.ErrClosed
			}
			n, err := c.writeWire(chunk)
			total += n
			p = p[n:]
			if err != nil {
				return total, err
			}
			if n == 0 {
				return total, io.ErrNoProgress
			}
		}
		return total, nil
	}
	return c.writeWire(p)
}

func (c *providerTestConn) writeWire(p []byte) (int, error) {
	if delay := time.Until(time.Unix(0, c.writeStallUntil.Load())); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-c.done:
			return 0, net.ErrClosed
		}
	}
	if len(p) > 0 && c.network != nil {
		if !c.network.downlink.waitUntil(len(p), c.done) {
			return 0, net.ErrClosed
		}
	}
	n, err := c.Conn.Write(p)
	if n > 0 && c.network != nil {
		c.network.downBytes.Add(uint64(n))
	}
	return n, err
}

func (c *providerTestConn) Close() error {
	if c.closed.CompareAndSwap(false, true) {
		if c.done != nil {
			close(c.done)
		}
		if c.network != nil {
			c.network.unregister(c.id)
		}
		return c.Conn.Close()
	}
	return nil
}

type providerTestListener struct {
	net.Listener
	network *providerTestNetwork
	account string
}

func (l providerTestListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if l.network != nil && !l.network.accountAvailable(l.account) {
			_ = c.Close()
			continue
		}
		return l.network.wrap(l.account, c), nil
	}
}

type providerCapStep struct {
	Mbps     float64
	Duration time.Duration
}

// parseProviderProfile accepts e.g. "100:10,50:10,25:10,10:10" where each
// pair is Mbps:seconds. A single number is also accepted as a fixed cap.
func parseProviderProfile(t *testing.T, raw string, fallback float64) []providerCapStep {
	t.Helper()
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []providerCapStep{{Mbps: fallback}}
	}
	parts := strings.Split(raw, ",")
	steps := make([]providerCapStep, 0, len(parts))
	for _, part := range parts {
		fields := strings.Split(strings.TrimSpace(part), ":")
		mbps, err := strconv.ParseFloat(fields[0], 64)
		if err != nil || mbps < 0 || mbps > 10000 {
			t.Fatalf("invalid provider Mbps in ITOM_LOAD_PROVIDER_PROFILE: %q", part)
		}
		step := providerCapStep{Mbps: mbps}
		if len(fields) == 2 {
			seconds, err := strconv.ParseFloat(fields[1], 64)
			if err != nil || seconds <= 0 || seconds > 3600 {
				t.Fatalf("invalid provider duration in ITOM_LOAD_PROVIDER_PROFILE: %q", part)
			}
			step.Duration = time.Duration(seconds * float64(time.Second))
		} else if len(fields) > 2 {
			t.Fatalf("invalid ITOM_LOAD_PROVIDER_PROFILE step: %q", part)
		}
		steps = append(steps, step)
	}
	return steps
}

type providerOutageSpec struct {
	Account  int
	Start    time.Duration
	Duration time.Duration
}

// parseProviderOutage accepts "account:start_seconds:duration_seconds", for
// example "3:12:5" to remove account 3 twelve seconds into measurement for
// five seconds. Empty disables the deterministic whole-account outage.
func parseProviderOutage(t *testing.T, raw string, accounts int) providerOutageSpec {
	t.Helper()
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return providerOutageSpec{}
	}
	fields := strings.Split(raw, ":")
	if len(fields) != 3 {
		t.Fatalf("ITOM_LOAD_ACCOUNT_OUTAGE must be account:start_seconds:duration_seconds")
	}
	account, err := strconv.Atoi(fields[0])
	if err != nil || account < 1 || account > accounts {
		t.Fatalf("ITOM_LOAD_ACCOUNT_OUTAGE account must be in [1,%d]", accounts)
	}
	start, err := strconv.ParseFloat(fields[1], 64)
	if err != nil || start < 0 || start > 3600 {
		t.Fatal("ITOM_LOAD_ACCOUNT_OUTAGE start_seconds must be in [0,3600]")
	}
	duration, err := strconv.ParseFloat(fields[2], 64)
	if err != nil || duration <= 0 || duration > 3600 {
		t.Fatal("ITOM_LOAD_ACCOUNT_OUTAGE duration_seconds must be in (0,3600]")
	}
	return providerOutageSpec{Account: account, Start: time.Duration(start * float64(time.Second)), Duration: time.Duration(duration * float64(time.Second))}
}

func checkParseProviderProfileVariableCaps(t *testing.T) {
	steps := parseProviderProfile(t, "100:2,50:3,25:4,10:5", 0)
	if len(steps) != 4 {
		t.Fatalf("steps=%d, want 4", len(steps))
	}
	wantMbps := []float64{100, 50, 25, 10}
	wantDur := []time.Duration{2 * time.Second, 3 * time.Second, 4 * time.Second, 5 * time.Second}
	for i := range steps {
		if steps[i].Mbps != wantMbps[i] || steps[i].Duration != wantDur[i] {
			t.Fatalf("step %d = %#v, want %.1f/%v", i, steps[i], wantMbps[i], wantDur[i])
		}
	}
}

func checkProviderRateLaneActuallyPacesBytes(t *testing.T) {
	var lane providerRateLane
	lane.setMbps(8) // 1 MB/s
	// The shaper permits one initial chunk as its bounded burst, then schedules
	// the next chunk behind the service time of that burst.
	lane.wait(256 << 10)
	started := time.Now()
	lane.wait(256 << 10)
	elapsed := time.Since(started)
	if elapsed < 220*time.Millisecond {
		t.Fatalf("rate cap did not pace bytes: elapsed=%v", elapsed)
	}
	if elapsed > 700*time.Millisecond {
		t.Fatalf("rate cap pacing unexpectedly slow: elapsed=%v", elapsed)
	}
}

func TestProviderAccountOutageDropsAllCurrentLinesAndRecovers(t *testing.T) {
	p := newProviderTestNetwork(0)
	left1, right1 := net.Pipe()
	left2, right2 := net.Pipe()
	defer right1.Close()
	defer right2.Close()
	c1 := p.wrap("a01", left1)
	c2 := p.wrap("a01", left2)
	if got := p.snapshot().Active; got != 2 {
		t.Fatalf("active=%d, want 2", got)
	}
	if dropped := p.outageAccount("a01", 40*time.Millisecond); dropped != 2 {
		t.Fatalf("dropped=%d, want 2", dropped)
	}
	if p.accountAvailable("a01") {
		t.Fatal("account available during outage")
	}
	_ = c1.Close()
	_ = c2.Close()
	time.Sleep(60 * time.Millisecond)
	if !p.accountAvailable("a01") {
		t.Fatal("account did not recover after outage")
	}
	if snap := p.snapshot(); snap.Dropped != 2 || snap.DownAccounts != 0 {
		t.Fatalf("unexpected recovery snapshot: %+v", snap)
	}
}

func TestProviderPacerCloseInterruptsReservedWait(t *testing.T) {
	p := newProviderTestNetwork(.01)
	a, b := net.Pipe()
	defer b.Close()
	c := p.wrap("a01", a).(*providerTestConn)
	// Reserve a multi-second future slot without sleeping on the first call.
	if !p.downlink.waitUntil(64<<10, c.done) {
		t.Fatal("initial reservation failed")
	}
	finished := make(chan error, 1)
	go func() { _, e := c.Write([]byte("second reservation")); finished <- e }()
	time.Sleep(20 * time.Millisecond)
	c.Close()
	select {
	case e := <-finished:
		if e == nil {
			t.Fatal("closed write succeeded")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("provider pacing survived Close")
	}
}

func (p *providerTestNetwork) markMobileClient(address, remote string) bool {
	if p == nil || address == "" || remote == "" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.active {
		if c.originalPeer == address && c.originalLocal == remote && !c.closed.Load() {
			c.mobileClient.Store(true)
			return true
		}
	}
	return false
}

// Resource-independent checks form one scenario; lifecycle and concurrency
// checks in this file remain independently scheduled.
func TestScenarioProviderNetwork(t *testing.T) {
	t.Log("check=checkParseProviderProfileVariableCaps")
	checkParseProviderProfileVariableCaps(t)
	t.Log("check=checkProviderRateLaneActuallyPacesBytes")
	checkProviderRateLaneActuallyPacesBytes(t)
}

func (p *providerTestNetwork) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
}
