package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"itom.local/core/socks"
	"itom.local/core/wire"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type Client struct {
	notices        noticeReceiver
	accountViews   map[string]*accountView
	ctx            context.Context
	cancel         context.CancelFunc
	mu             sync.Mutex
	lanes          map[string]*lane
	wg             sync.WaitGroup
	turn           int
	total          int
	accountIDs     []string
	diagnostics    map[string]*diagnostic
	events         []string
	tracer         *traceLog
	stripe         *stripeGroup
	logicalSession wire.ID
	generation     uint64
}
type Stats struct {
	ActiveLines, TotalLines, TCP int
	RTTMillis                    int64
}

// openLaneScore ranks only already-healthy owner lanes. OPEN/OPEN_OK is lane-bound,
// so assigning a new TCP connection round-robin to a 2 s carrier while a sub-second
// sibling is fresh can turn one congested IMAP path into a multi-second application
// stall. Prefer recent, low-RTT, lightly queued lanes while retaining a small active
// stream penalty so all new connections do not collapse onto one carrier.
func openLaneScore(l *lane, now time.Time) int64 { return openLaneScoreWithEstimate(l, now, 1200) }
func openLaneScoreWithEstimate(l *lane, now time.Time, estimate int64) int64 {
	if l == nil {
		return 1 << 62
	}
	rtt := l.rtt.Load()
	if rtt <= 0 {
		rtt = estimate
	} else if rtt > 5000 {
		rtt = 5000
	}
	age := l.remoteProgressAge(now).Milliseconds()
	if age < 0 {
		age = 0
	} else if age > 5000 {
		age = 5000
	}
	queue := int64(len(l.urgentQueue))*80 + int64(len(l.controlQueue))*15 + int64(len(l.maintenanceQueue))*5
	owner := l
	if l.stripe != nil {
		if stable := l.stripe.accountOwner(l.account.ID); stable != nil {
			owner = stable
		}
	}
	active := max(owner.active.Load(), owner.admitted.Load()) * 30
	return rtt + age/2 + queue + active
}

type ClientOptions struct {
	Diagnostics bool
	// Optional fixture observer; it cannot alter carrier admission or retry.
	onCarrierExit func(*lane, error)
}

func StartClient(parent context.Context, p servercfg.Profile, dir string, dial Dial) (*Client, error) {
	return StartClientWithOptions(parent, p, dir, dial, ClientOptions{Diagnostics: true})
}

func StartClientWithOptions(parent context.Context, p servercfg.Profile, dir string, dial Dial, options ClientOptions) (*Client, error) {
	cfg := servercfg.Config{Schema: 1, Server: p.Server, Provider: p.Provider, Accounts: p.Accounts, Users: []servercfg.User{p.User}, Transport: p.Transport}
	if e := cfg.Validate(); e != nil {
		return nil, e
	}
	if len(p.Accounts) == 0 || !p.User.Enabled {
		return nil, errors.New("PROFILE_NOT_READY")
	}
	if dial == nil {
		dial = mailbox.Dial
	}
	policy := p.Transport.Effective()
	sockets := newSocketAdmission(policy.EndpointConnections, policy.EndpointAccountSessions(false), policy.LoginParallelism)
	dial = sockets.wrap(dial)
	activation := newAccountAdmission(policy.EndpointConnections, min(5, policy.EndpointAccountSessions(false)))
	ctx, cancel := context.WithCancel(parent)
	logicalSession, generation, attachErr := freshAttachment()
	if attachErr != nil {
		cancel()
		return nil, attachErr
	}
	c := &Client{ctx: ctx, cancel: cancel, lanes: map[string]*lane{}, total: len(p.Accounts) * 2, diagnostics: map[string]*diagnostic{}, stripe: newStripeGroup(logicalSession, generation), logicalSession: logicalSession, generation: generation}
	c.accountViews = map[string]*accountView{}
	for _, a := range p.Accounts {
		c.accountViews[a.ID] = &accountView{}
	}
	if options.Diagnostics {
		c.tracer, _ = newTrace(dir)
	}
	c.stripe.configuredAccounts = len(p.Accounts)
	for _, a := range p.Accounts {
		c.accountIDs = append(c.accountIDs, a.ID)
	}
	for accountIndex, a := range p.Accounts {
		var d *diagnostic
		if options.Diagnostics {
			d = &diagnostic{tracer: c.tracer, account: a.ID, user: p.User.ID, side: "client", line: LineDiagnostic{Account: a.ID, Stage: "connecting"}}
			d.log = func(message string) {
				c.mu.Lock()
				defer c.mu.Unlock()
				c.events = append(c.events, time.Now().UTC().Format(time.RFC3339)+" account="+a.ID+" "+message)
				if len(c.events) > 160 {
					c.events = c.events[len(c.events)-160:]
				}
			}
			if c.tracer == nil {
				d.event("event=trace_unavailable code=TRACE_OPEN_FAILED")
			}
			c.diagnostics[a.ID] = d
		}
		if d == nil {
			d = &diagnostic{metricsOnly: true, account: a.ID, user: p.User.ID, side: "client", line: LineDiagnostic{Account: a.ID, Stage: "connecting"}}
			c.diagnostics[a.ID] = d
		}
		c.wg.Add(1)
		go func(a servercfg.Account) {
			defer c.wg.Done()
			firstActivation := true
			var retry carrierRetryPolicy
			var carrierEpoch uint64
			for ctx.Err() == nil {
				if d != nil {
					d.mu.Lock()
					d.line.Stage = "standby"
					d.mu.Unlock()
				}
				var admissionErr error
				if firstActivation {
					admissionErr = activation.acquireInitial(ctx, accountIndex)
					firstActivation = false
				} else {
					admissionErr = activation.acquire(ctx)
				}
				if admissionErr != nil {
					return
				}
				wasAuthenticated := false
				carrierEpoch++
				started := time.Now()
				if d != nil {
					d.beginConnect()
				}
				mail, e := openLaneMailFastStart(ctx, dial, a.Mail, topologyFolders(p.User, 4))
				if d != nil {
					d.connected(started, e)
				}
				if e == nil {
					mail.topology = policy
					mail.writerTarget = 1
					logical, attachGeneration, stripe := c.attachment()
					var l *lane
					var transport mailbox.Transport = mail
					if d != nil {
						transport = &observedMail{mail, d}
					}
					l, e = newLaneAttached(ctx, p.Server, p.User, a, filepath.Join(dir, a.ID), false, transport, d, logical, attachGeneration, carrierEpoch)
					if e == nil {
						l.notices = &c.notices
						l.accountView = c.accountViews[a.ID]
						e = stripe.attach(l)
					}
					if e == nil {
						e = l.reconcileRecoveredCrossOwners()
					}
					if e == nil {
						c.mu.Lock()
						if c.stripe != stripe {
							e = errStripeUnavailable
						} else {
							c.lanes[a.ID] = l
						}
						c.mu.Unlock()
					}
					if e == nil {
						e = l.run()
						wasAuthenticated = l.everReady.Load()
						if options.onCarrierExit != nil {
							options.onCarrierExit(l, e)
						}
						c.mu.Lock()
						if c.lanes[a.ID] == l {
							delete(c.lanes, a.ID)
						}
						c.mu.Unlock()
					} else if l != nil {
						// newLaneAttached starts bounded control I/O before returning.
						// If stripe admission rejects this carrier, close the lane rather
						// than leaking that lifetime until the whole profile stops.
						l.close()
					}
					mail.Close()
				}
				activation.release()
				delay := retry.next(time.Now(), wasAuthenticated)
				if ctx.Err() == nil && d != nil {
					d.reconnect(e, delay)
				}
				if pause(ctx, delay) != nil {
					return
				}
			}
		}(a)
	}
	return c, nil
}

func (c *Client) attachment() (wire.ID, uint64, *stripeGroup) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.logicalSession, c.generation, c.stripe
}

// rotateAttachment starts a fresh authenticated logical session after the old
// monotonic stream-ID space is exhausted. IDs are never reused inside one
// session; existing carrier supervisors are simply nudged to reconnect against
// the new tuple only once all TCP sockets are closed. Exhausted metadata must
// reject a new OPEN rather than tear down unrelated active connections.
func (c *Client) rotateAttachment(expected *stripeGroup) error {
	logical, generation, err := freshAttachment()
	if err != nil {
		return err
	}
	c.mu.Lock()
	if expected != nil && c.stripe != expected {
		c.mu.Unlock()
		return nil // another caller already rotated this exhausted session
	}
	c.stripe.mu.Lock()
	if c.stripe.active.Load() != 0 {
		c.stripe.mu.Unlock()
		c.mu.Unlock()
		return socks.OpenError{Code: wire.ReasonCapacity}
	}
	c.stripe.stopped = true
	c.stripe.mu.Unlock()
	old := make([]*lane, 0, len(c.lanes))
	for _, l := range c.lanes {
		if l != nil {
			old = append(old, l)
		}
	}
	oldGroup := c.stripe
	c.logicalSession = logical
	c.generation = generation
	c.stripe = newStripeGroup(logical, generation)
	c.stripe.configuredAccounts = c.total / 2
	c.lanes = map[string]*lane{}
	c.events = append(c.events, time.Now().UTC().Format(time.RFC3339)+" event=logical_session_rotate reason=STREAM_ID_EXHAUSTED")
	if len(c.events) > 160 {
		c.events = c.events[len(c.events)-160:]
	}
	c.mu.Unlock()
	for _, l := range old {
		l.cancel()
	}
	oldGroup.stopOwners()
	oldGroup.ownerWG.Wait()
	return nil
}

func (c *Client) Close() {
	c.cancel()
	c.wg.Wait()
	c.stripe.stopOwners()
	c.stripe.ownerWG.Wait()
	if c.tracer != nil {
		c.tracer.Close()
	}
}

// ClientSnapshot samples readiness once for both aggregate and per-account UI.
// Stage records lifecycle history; Ready/ReadinessReason describe current use.
type ClientSnapshot struct {
	ServerMessage         string
	ServerMessageRevision uint64
	Stats
	SampledAtMS int64
	Lines       []LineDiagnostic
	Events      []string
	Trace       traceStatus
}

func (c *Client) Stats() Stats { return c.Snapshot().Stats }

func (c *Client) Snapshot() ClientSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	snapshot := ClientSnapshot{Stats: Stats{TotalLines: c.total}, SampledAtMS: now.UnixMilli(), Lines: make([]LineDiagnostic, 0, len(c.accountIDs))}
	notice := c.notices.snapshot()
	snapshot.ServerMessage = notice.Text
	snapshot.ServerMessageRevision = notice.Revision
	ids := c.accountIDs
	// Unit fixtures and older callers may populate lanes without a profile.
	if len(ids) == 0 {
		for id := range c.diagnostics {
			ids = append(ids, id)
		}
		for id := range c.lanes {
			if c.diagnostics[id] == nil {
				ids = append(ids, id)
			}
		}
	}
	var measured int64
	for _, id := range ids {
		line := LineDiagnostic{Account: id, Stage: "connecting", TotalLines: 2}
		if d := c.diagnostics[id]; d != nil {
			line = d.snapshot()
			line.Extended = !d.metricsOnly
			line.TotalLines = 2
		}
		l := c.lanes[id]
		if a := c.accountViews[id]; a != nil {
			line.UploadBytes = a.upload.Load()
			line.DownloadBytes = a.download.Load()
		}
		if c.stripe != nil {
			line.TCPStreams = c.stripe.carrierStreamCount(id)
		}
		if l != nil {
			applyRTTDiagnostics(&line, l, now)
			line.RXAgeMS = l.remoteProgressAge(now).Milliseconds()
		}
		line.Ready = l != nil && l.openHealthy(now)
		dataRecovering := false
		if line.Ready {
			if capacity, ok := l.mail.(interface{ DataAvailable() bool }); ok && !capacity.DataAvailable() {
				line.Ready = false
				dataRecovering = true
			}
		}
		line.ActiveLines = 0
		line.ReadinessReason = "connecting"
		if line.Ready {
			line.ActiveLines = 2
			line.ReadinessReason = "ready"
			snapshot.ActiveLines += line.ActiveLines
			if r := line.RTTMillis; r > 0 && line.RTTFresh {
				snapshot.RTTMillis += r
				measured++
			}
		} else if dataRecovering {
			line.ReadinessReason = "data_recovering"
		} else if l == nil {
			if line.Stage != "ready" && line.Stage != "" {
				line.ReadinessReason = line.Stage
			} else {
				line.ReadinessReason = "backoff"
			}
		} else {
			switch {
			case l.ctx.Err() != nil:
				line.ReadinessReason = "backoff"
			case l.rejoinPending.Load():
				line.ReadinessReason = "reattaching"
			case !l.ready.Load():
				line.ReadinessReason = "grants"
			case now.UnixNano() < l.openQuarantineUntil.Load():
				line.ReadinessReason = "open_quarantine"
			default:
				line.ReadinessReason = "reader_stale"
			}
		}
		snapshot.Lines = append(snapshot.Lines, line)
	}
	sort.Slice(snapshot.Lines, func(i, j int) bool { return snapshot.Lines[i].Account < snapshot.Lines[j].Account })
	if c.stripe != nil {
		c.stripe.mu.Lock()
		for _, owner := range c.stripe.logicalOwners {
			snapshot.TCP += int(owner.active.Load())
		}
		c.stripe.mu.Unlock()
	}
	if measured > 0 {
		snapshot.RTTMillis /= measured
	}
	snapshot.Events = append([]string(nil), c.events...)
	if c.tracer != nil {
		snapshot.Trace = c.tracer.status()
	}
	return snapshot
}

func (c *Client) Open(ctx context.Context, target wire.Connect) (socks.Stream, netip.AddrPort, error) {
	rotating := false
	for {
		c.mu.Lock()
		var ready []*lane
		capacityBlocked := false
		keys := make([]string, 0, len(c.lanes))
		for id := range c.lanes {
			keys = append(keys, id)
		}
		sort.Strings(keys)
		now := time.Now()
		for _, id := range keys {
			l := c.lanes[id]
			if l.openHealthy(now) {
				owner := l
				if l.stripe != nil {
					owner = l.stripe.accountOwner(l.account.ID)
				}
				if owner != nil && max(owner.admitted.Load(), owner.active.Load()) >= runtimeOwnerStreams {
					capacityBlocked = true
					continue
				}
				ready = append(ready, l)
			}
		}
		if len(ready) == 0 {
			c.mu.Unlock()
			if capacityBlocked {
				return nil, netip.AddrPort{}, socks.OpenError{Code: wire.ReasonCapacity}
			}
			if !rotating {
				return nil, netip.AddrPort{}, errors.New("NO_HEALTHY_LINES")
			}
			select {
			case <-ctx.Done():
				return nil, netip.AddrPort{}, ctx.Err()
			case <-c.ctx.Done():
				return nil, netip.AddrPort{}, c.ctx.Err()
			case <-time.After(20 * time.Millisecond):
				continue
			}
		}
		eligible := rankOpenLanes(ready, now)
		l := ready[c.turn%eligible]
		c.turn++
		owner, id, err := l.reserveOpen()
		c.mu.Unlock()
		var conn *pipe
		if err == nil {
			conn, err = l.openReserved(ctx, target, owner, id)
		}
		if errors.Is(err, errLaneUnhealthy) || (err != nil && ctx.Err() == nil && l.ctx.Err() != nil) {
			continue
		}
		if !errors.Is(err, errStripeSessionLimit) {
			if err != nil {
				return nil, netip.AddrPort{}, err
			}
			return conn, netip.AddrPort{}, nil
		}
		if err = c.rotateAttachment(l.stripe); err != nil {
			return nil, netip.AddrPort{}, err
		}
		rotating = true
	}
}

// serverOptions keeps fixture injection on the production supervisor path.
// No alternate lane lifecycle is maintained by tests.
type serverOptions struct {
	readNotice    func() (servercfg.Config, error)
	dial          Dial
	allowPrivate  bool
	diagnostics   bool
	onLane        func(*lane)
	onCarrierExit func(*lane, error)
}

func Serve(ctx context.Context, cfg servercfg.Config, dir string, log func(string)) error {
	return serveWithOptions(ctx, cfg, dir, log, serverOptions{dial: mailbox.Dial, diagnostics: cfg.DiagnosticsMode != "off", readNotice: noticeSource(dir)})
}
func serveWithOptions(ctx context.Context, cfg servercfg.Config, dir string, log func(string), options serverOptions) error {
	if cfg.DiagnosticsMode == "off" {
		options.diagnostics = false
		log = nil
	}
	if options.dial == nil {
		options.dial = mailbox.Dial
	}
	if log == nil {
		log = func(string) {}
	}
	if e := cfg.Validate(); e != nil {
		return e
	}
	policy := cfg.TransportForUsers()
	if err := policy.Validate(); err != nil {
		return err
	}
	sockets := newSocketAdmission(policy.EndpointConnections, policy.EndpointAccountSessions(true), policy.LoginParallelism)
	options.dial = sockets.wrap(options.dial)
	activation := newAccountAdmission(policy.EndpointConnections, min(1+4*policy.Participants, policy.EndpointAccountSessions(true)))
	traffic := newServerTraffic(cfg)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	trafficPath := filepath.Join(dir, TrafficFile)
	if err := writeTraffic(trafficPath, traffic.snapshot(true)); err != nil {
		return err
	}
	trafficCtx, trafficCancel := context.WithCancel(ctx)
	trafficDone := make(chan struct{})
	go func() { defer close(trafficDone); traffic.publish(trafficCtx, trafficPath) }()
	defer func() {
		trafficCancel()
		<-trafficDone
		_ = writeTraffic(trafficPath, traffic.snapshot(false))
	}()
	var tracer *traceLog
	var traceErr error
	if options.diagnostics {
		tracer, traceErr = newTrace(dir)
	}
	defer tracer.Close()
	if traceErr != nil {
		log("event=trace_unavailable code=TRACE_OPEN_FAILED")
	}
	var wg sync.WaitGroup
	count := 0
	hubs := map[string]*stripeHub{}
	cleanupGate := make(chan struct{}, 1)
	cleanupGate <- struct{}{}
	for _, u := range cfg.Users {
		if u.Enabled {
			hubs[u.ID] = newStripeHub()
		}
	}
	readNotice := options.readNotice
	if readNotice == nil {
		readNotice = func() (servercfg.Config, error) { return cfg, nil }
	}
	for _, u := range cfg.Users {
		if u.Enabled {
			u := u
			wg.Add(1)
			go func(u servercfg.User) {
				defer wg.Done()
				runFallbackMailJanitorWithDial(ctx, cfg, u, hubs[u.ID], cleanupGate, log, options.dial)
			}(u)
		}
	}

	for accountIndex, a := range cfg.Accounts {
		folders := make([]string, 0, len(cfg.Users)*2)
		for _, u := range cfg.Users {
			if u.Enabled {
				folders = append(folders, topologyFolders(u, 4)...)
			}
		}
		if len(folders) == 0 {
			continue
		}
		a := a
		users := make([]string, 0, len(cfg.Users))
		for _, u := range cfg.Users {
			if u.Enabled {
				users = append(users, u.ID)
			}
		}
		count += len(users)
		wg.Add(1)
		go func(a servercfg.Account, users, folders []string) {
			defer wg.Done()
			rootContext := ctx
			firstActivation := true
			for rootContext.Err() == nil {
				var admissionErr error
				if firstActivation {
					admissionErr = activation.acquireInitial(rootContext, accountIndex)
					firstActivation = false
				} else {
					admissionErr = activation.acquire(rootContext)
				}
				if admissionErr != nil {
					return
				}
				ctx, stopAccount := context.WithCancel(rootContext)
				var physicalCarriers sync.Map
				watchDone := make(chan struct{})
				go func() {
					defer close(watchDone)
					ticker := time.NewTicker(5 * time.Second)
					defer ticker.Stop()
					lastHealthy := time.Now()
					for {
						select {
						case <-ctx.Done():
							return
						case now := <-ticker.C:
							healthy := false
							physicalCarriers.Range(func(key, value any) bool {
								l := key.(*lane)
								healthy = healthy || l.streamCarrierReady() && l.ctx.Err() == nil
								return !healthy
							})
							if healthy {
								lastHealthy = now
							} else if len(cfg.Accounts) > cap(activation.slots) && now.Sub(lastHealthy) >= 30*time.Second {
								stopAccount()
								return
							}
						}
					}
				}()
				// Reserve the shared DATA writer before optional sockets can
				// consume its account budget. Low-session layouts intentionally
				// use the existing per-lane base writer instead.
				var seeds []appendClient
				if policy.EndpointAccountSessions(true) > policy.Participants {
					if writer, err := options.dial(ctx, a.Mail, folders); err == nil {
						seeds = []appendClient{writer}
					}
				}
				runtime := newAccountRuntimeSeeded(ctx, users, func(callCtx context.Context) (appendClient, error) { return options.dial(callCtx, a.Mail, folders) }, 1, seeds)

				var accountWG sync.WaitGroup
				for _, u := range cfg.Users {
					if !u.Enabled {
						continue
					}
					accountWG.Add(1)
					go func(a servercfg.Account, u servercfg.User, runtime *accountRuntime, hub *stripeHub) {
						defer accountWG.Done()
						var d *diagnostic
						if options.diagnostics {
							d = &diagnostic{tracer: tracer, account: a.ID, user: u.ID, side: "server", line: LineDiagnostic{Account: a.ID, Stage: "connecting"}, log: func(message string) { log(fmt.Sprintf("account=%s user=%s %s", a.ID, u.ID, message)) }}
							d.event("event=connecting")
						}
						var retry carrierRetryPolicy
						for ctx.Err() == nil {
							wasAuthenticated := false
							if waitErr := hub.waitCleanup(ctx); waitErr != nil {
								return
							}
							start := time.Now()
							if d != nil {
								d.beginConnect()
							}
							m, e := openLaneMailShared(ctx, options.dial, a.Mail, topologyFolders(u, 4), runtime, u.ID)
							if d != nil {
								d.connected(start, e)
							}
							if e == nil {
								m.topology = policy
								if len(seeds) == 0 {
									m.runtime = nil
								}
								m.writerTarget = 1
								var l *lane
								var transport mailbox.Transport = m
								if options.diagnostics {
									transport = &observedMail{m, d}
								}
								l, e = newLaneLogged(ctx, cfg.Server, u, a, filepath.Join(dir, a.ID, u.ID), true, transport, d)
								if e == nil {
									l.ownerCancel()
									l.ownerCtx, l.ownerCancel = context.WithCancel(rootContext)
									physicalCarriers.Store(l, true)
									l.traffic = traffic.users[u.ID]
									e = hub.attach(l)
								}
								if e == nil {
									e = l.reconcileRecoveredCrossOwners()
								}
								if e == nil {
									l.allowPrivate = options.allowPrivate
									if options.onLane != nil {
										options.onLane(l)
									}
									if d != nil {
										d.event("event=authenticated")
									}
									e = l.run()
									wasAuthenticated = l.everReady.Load()
									if options.onCarrierExit != nil {
										options.onCarrierExit(l, e)
									}
								} else if l != nil {
									l.close()
								}
								physicalCarriers.Delete(l)
								m.Close()
							}
							if ctx.Err() != nil {
								return
							}
							delay := retry.next(time.Now(), wasAuthenticated)
							if d != nil {
								d.reconnect(e, delay)
							}
							if pause(ctx, delay) != nil {
								return
							}
						}
					}(a, u, runtime, hubs[u.ID])
				}
				accountWG.Wait()
				stopAccount()
				<-watchDone
				runtime.Close()
				activation.release()
				if pause(rootContext, time.Second) != nil {
					return
				}
			}
		}(a, users, folders)
	}
	if count == 0 {
		return errors.New("CONFIG_NOT_READY")
	}
	wg.Add(1)
	go func() { defer wg.Done(); runServerNotices(ctx, hubs, readNotice) }()
	wg.Wait()
	for _, hub := range hubs {
		hub.mu.Lock()
		groups := make([]*stripeGroup, 0, len(hub.groups))
		for _, g := range hub.groups {
			groups = append(groups, g)
		}
		hub.mu.Unlock()
		for _, g := range groups {
			g.stopOwners()
			g.ownerWG.Wait()
		}
	}
	return ctx.Err()
}

func (c *Client) Diagnostics() string {
	b, _ := json.Marshal(c.Snapshot())
	return string(b)
}

// Missing RTT is uncertainty, not evidence of a slow authenticated carrier.
func rankOpenLanes(ready []*lane, now time.Time) int {
	var samples []int64
	for _, l := range ready {
		if rtt := l.rtt.Load(); rtt > 0 {
			samples = append(samples, min(rtt, int64(5000)))
		}
	}
	estimate := int64(1200)
	if len(samples) > 0 {
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		estimate = samples[(len(samples)-1)/2]
	}
	scores := map[*lane]int64{}
	for _, l := range ready {
		scores[l] = openLaneScoreWithEstimate(l, now, estimate)
	}
	sort.SliceStable(ready, func(i, j int) bool {
		if scores[ready[i]] == scores[ready[j]] {
			return ready[i].account.ID < ready[j].account.ID
		}
		return scores[ready[i]] < scores[ready[j]]
	})
	if len(ready) == 0 {
		return 0
	}
	best := scores[ready[0]]
	margin := max(int64(250), best/4)
	eligible := 1
	for eligible < len(ready) && eligible < 3 && scores[ready[eligible]] <= best+margin {
		eligible++
	}
	return eligible
}

// RecordNetworkEvent correlates Android callbacks with socket failures without
// recording addresses, DNS servers, profile contents or credential text.
func (c *Client) RecordNetworkEvent(kind string, handle int64) {
	switch kind {
	case "available", "lost", "capabilities", "link_properties", "selected", "initial", "session_stop":
	case "selected_wifi", "selected_mobile", "selected_other", "selected_none",
		"initial_wifi", "initial_mobile", "initial_other", "initial_none",
		"auto_stop_wifi", "auto_stop_mobile", "auto_stop_other", "auto_stop_none":
	default:
		return
	}
	c.mu.Lock()
	tracer := c.tracer
	c.mu.Unlock()
	if tracer != nil {
		details, _ := json.Marshal(struct {
			Handle int64 `json:"network_handle"`
		}{handle})
		tracer.emit(traceEvent{Event: "underlying_network", Name: kind, Side: "client", Details: details})
	}
}
