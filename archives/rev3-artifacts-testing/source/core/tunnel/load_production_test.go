package tunnel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
)

// TestLoadedApplicationProductionHarness is intentionally opt-in. The normal
// User speed/RTT alternatives apply to TestLoadedApplicationRTT and
// TestLoadedApplicationUploadWithTrace. This harness exists to measure the
// production shared-account path over a fixed interval without turning an
// exploratory capacity test into a release criterion.
//
// Example:
//
//	ITOM_LONG_LOAD=1 ITOM_LOAD_ACCOUNTS=10 ITOM_LOAD_USERS=2 \
//	ITOM_LOAD_STREAMS=4 ITOM_LOAD_SECONDS=30 go test ./tunnel \
//	-run TestLoadedApplicationProductionHarness -count=1 -v
//
// ITOM_LOAD_MIN_MBPS and ITOM_LOAD_MAX_RTT_MS are optional qualification
// assertions for this exploratory path, not release qualification. The
// supervisor path always applies the user speed/RTT tiers. With default 0
// this exploratory path only verifies integrity,
// overlap, runtime health and bounded completion.
func TestLoadedApplicationProductionHarness(t *testing.T) {
	if os.Getenv("ITOM_LONG_LOAD") != "1" {
		t.Skip("set ITOM_LONG_LOAD=1 to run the production-path capacity harness")
	}
	runProductionLoad(t)
}

func runProductionLoad(t *testing.T) {

	if os.Getenv("ITOM_LOAD_SUPERVISORS") == "1" {
		testSupervisedProductionLoad(t)
		return
	}
	accounts := loadEnvInt(t, "ITOM_LOAD_ACCOUNTS", 4, 1, 10)
	usersN := loadEnvInt(t, "ITOM_LOAD_USERS", 1, 1, 2)
	streams := loadEnvInt(t, "ITOM_LOAD_STREAMS", 4, 1, 32)
	seconds := loadEnvInt(t, "ITOM_LOAD_SECONDS", 30, 5, 180)
	warmupSeconds := loadEnvInt(t, "ITOM_LOAD_WARMUP_SECONDS", 10, 1, 30)
	commandDelayMS := loadEnvInt(t, "ITOM_LOAD_COMMAND_DELAY_MS", 80, 0, 5000)
	diagnosticsEnabled := loadEnvInt(t, "ITOM_LOAD_DIAGNOSTICS", 0, 0, 1) == 1
	minMbps := loadEnvFloat(t, "ITOM_LOAD_MIN_MBPS", 0)
	maxRTTMS := loadEnvFloat(t, "ITOM_LOAD_MAX_RTT_MS", 0)
	traceEnabled := os.Getenv("ITOM_LOAD_TRACE") == "1"
	providerMbps := loadEnvFloat(t, "ITOM_LOAD_PROVIDER_MBPS", 0)
	providerProfile := parseProviderProfile(t, os.Getenv("ITOM_LOAD_PROVIDER_PROFILE"), providerMbps)
	faultSeed := int64(loadEnvInt(t, "ITOM_LOAD_FAULT_SEED", 1, 0, 1<<30))
	randomDropMinMS := loadEnvInt(t, "ITOM_LOAD_RANDOM_DROP_MIN_MS", 0, 0, 600000)
	randomDropMaxMS := loadEnvInt(t, "ITOM_LOAD_RANDOM_DROP_MAX_MS", 0, 0, 600000)
	if (randomDropMinMS == 0) != (randomDropMaxMS == 0) || randomDropMaxMS < randomDropMinMS {
		t.Fatal("ITOM_LOAD_RANDOM_DROP_MIN_MS/MAX_MS must both be zero or satisfy 0 < min <= max")
	}
	accountOutage := parseProviderOutage(t, os.Getenv("ITOM_LOAD_ACCOUNT_OUTAGE"), accounts)
	if traceEnabled && !diagnosticsEnabled {
		t.Fatal("ITOM_LOAD_TRACE=1 requires ITOM_LOAD_DIAGNOSTICS=1")
	}
	repeats := loadEnvInt(t, "ITOM_LOAD_REPEATS", 1, 1, 10)
	measurementTarget := time.Duration(seconds) * time.Second
	probeDeadline := time.Duration(loadEnvInt(t, "ITOM_LOAD_PROBE_DEADLINE_MS", 1000, 100, 5000)) * time.Millisecond
	maxGap := time.Duration(loadEnvInt(t, "ITOM_LOAD_MAX_GAP_SECONDS", 5, 1, 25)) * time.Second
	phase := "setup"
	t.Logf("production_config accounts=%d users=%d streams=%d measurement_s=%d warmup_s=%d command_delay_ms=%d diagnostics=%t trace=%t provider_mbps=%.3f provider_profile=%q outage=%q probe_deadline_ms=%d max_gap_ms=%d", accounts, usersN, streams, seconds, warmupSeconds, commandDelayMS, diagnosticsEnabled, traceEnabled, providerMbps, os.Getenv("ITOM_LOAD_PROVIDER_PROFILE"), os.Getenv("ITOM_LOAD_ACCOUNT_OUTAGE"), probeDeadline.Milliseconds(), maxGap.Milliseconds())
	t.Log("production_phase phase=setup event=begin deadline_s=180")

	ctx, cancel := context.WithTimeout(context.Background(), measurementTarget*time.Duration(repeats)+4*time.Minute)
	defer cancel()
	cfg, err := servercfg.New("production-load")
	if err != nil {
		t.Fatal(err)
	}
	users := make([]servercfg.User, 0, usersN)
	for i := 0; i < usersN; i++ {
		u, addErr := cfg.AddUser(fmt.Sprintf("load-%d", i+1))
		if addErr != nil {
			t.Fatal(addErr)
		}
		users = append(users, u)
	}
	folders := make([]string, 0, usersN*2)
	userIDs := make([]string, 0, usersN)
	for _, u := range users {
		folders = append(folders, u.Up, u.Down)
		userIDs = append(userIDs, u.ID)
	}

	type userRuntime struct {
		logical    wire.ID
		generation uint64
		client     *stripeGroup
		server     *stripeHub
		lanes      []*lane
		clientDiag []*diagnostic
		serverDiag []*diagnostic
		target     *productionLoadTarget
	}
	perUser := make([]userRuntime, usersN)
	dumpFailure := func() {
		if !t.Failed() {
			return
		}
		t.Logf("production_failure phase=%s", phase)
		for i := range perUser {
			if perUser[i].target != nil {
				t.Logf("failure_progress user=%d upload=%d download=%d", i+1, perUser[i].target.uploadDelivered.Load(), perUser[i].target.downloadDelivered.Load())
			}
			for _, l := range perUser[i].lanes {
				if l.data == nil {
					continue
				}
				held, e := l.data.Held(l.account.ID, l.user.ID)
				grants, _ := l.data.ScanGrants(0, 256)
				attempts, _ := l.data.ScanAttempts(0, 256)
				t.Logf("failure_debt user=%d account=%s held=%v grants=%d attempts=%d err=%v", i+1, l.account.ID, held, len(grants), len(attempts), e)
			}
			if diagnosticsEnabled {
				productionLogDiagnostics(t, "client", i+1, perUser[i].clientDiag)
				productionLogDiagnostics(t, "server", i+1, perUser[i].serverDiag)
			}
		}
		stacks := make([]byte, 128<<10)
		n := runtime.Stack(stacks, true)
		t.Logf("failure_stacks bounded_bytes=%d\n%s", n, stacks[:n])
	}
	defer dumpFailure()
	setupTimer := time.AfterFunc(180*time.Second, cancel)
	defer setupTimer.Stop()
	for i := range perUser {
		logical, generation, attachErr := freshAttachment()
		if attachErr != nil {
			t.Fatal(attachErr)
		}
		perUser[i].logical = logical
		perUser[i].generation = generation
		perUser[i].client = newStripeGroup(logical, generation)
		perUser[i].server = newStripeHub()
		perUser[i].target = newProductionLoadTarget(t, fmt.Sprintf("%x/%s", logical, users[i].ID))
	}

	runtimeFailures := make(chan error, accounts*usersN*2+8)
	serverDir := t.TempDir()
	clientDir := t.TempDir()
	var serverTrace, clientTrace *traceLog
	if traceEnabled {
		serverTrace, err = newTrace(serverDir)
		if err != nil {
			t.Fatal(err)
		}
		defer serverTrace.Close()
		clientTrace, err = newTrace(clientDir)
		if err != nil {
			t.Fatal(err)
		}
		defer clientTrace.Close()
	}

	providerNetwork := newProviderTestNetwork(providerProfile[0].Mbps)
	runtimes := make([]*accountRuntime, 0, accounts)
	for accountIndex := 0; accountIndex < accounts; accountIndex++ {
		a := servercfg.Account{ID: fmt.Sprintf("a%02d", accountIndex+1), Mail: mailbox.Config{Host: "mail.example", Port: 993, TLS: "tls", Username: "u", Password: "p"}}
		clientSeedTransport, serverSeedTransport := tlsMailPairWithNetwork(t, ctx, folders, providerNetwork, a.ID)
		clientSeed, ok := clientSeedTransport.(*laneMail)
		if !ok {
			t.Fatal("test client seed is not laneMail")
		}
		serverSeed, ok := serverSeedTransport.(*laneMail)
		if !ok {
			t.Fatal("test server seed is not laneMail")
		}
		// Capture the raw dial before lane.Activate wraps it with the shared
		// account gate. Server DATA workers already acquire runtime.gate in
		// newAccountRuntime: using the later wrapped dial deadlocks on the
		// non-reentrant gate and turns download into an artificial zero.
		serverRawDial := serverSeed.dial
		runtime := newAccountRuntime(ctx, userIDs, func(callCtx context.Context) (appendClient, error) {
			return serverRawDial(callCtx)
		})
		runtimes = append(runtimes, runtime)

		for userIndex, u := range users {
			var serverMail *laneMail
			if userIndex == 0 {
				serverMail = serverSeed
			} else {
				base, dialErr := serverRawDial(ctx)
				if dialErr != nil {
					t.Fatal(dialErr)
				}
				serverMail = &laneMail{Client: base, dial: serverRawDial}
				defer serverMail.Close()
			}
			serverMail.runtime = runtime
			serverMail.runtimeUser = u.ID
			var sd *diagnostic
			var serverTransport mailbox.Transport = serverMail
			if diagnosticsEnabled {
				sd = &diagnostic{tracer: serverTrace, account: a.ID, user: u.ID, side: "server"}
				serverTransport = &observedMail{serverMail, sd}
				perUser[userIndex].serverDiag = append(perUser[userIndex].serverDiag, sd)
			}
			go func(a servercfg.Account, u servercfg.User, transport mailbox.Transport, d *diagnostic, hub *stripeHub) {
				l, laneErr := newLaneLogged(ctx, cfg.Server, u, a, filepath.Join(serverDir, a.ID, u.ID), true, transport, d)
				if laneErr == nil {
					l.allowPrivate = true
					laneErr = hub.attach(l)
				}
				if laneErr == nil {
					laneErr = l.run()
				} else if l != nil {
					l.close()
				}
				runtimeFailures <- laneErr
			}(a, u, serverTransport, sd, perUser[userIndex].server)

			var clientMail *laneMail
			if userIndex == 0 {
				clientMail = clientSeed
			} else {
				base, dialErr := clientSeed.dial(ctx)
				if dialErr != nil {
					t.Fatal(dialErr)
				}
				clientMail = &laneMail{Client: base, dial: clientSeed.dial}
				defer clientMail.Close()
			}
			var cd *diagnostic
			var clientTransport mailbox.Transport = clientMail
			if diagnosticsEnabled {
				cd = &diagnostic{tracer: clientTrace, account: a.ID, user: u.ID, side: "client"}
				clientTransport = &observedMail{clientMail, cd}
				perUser[userIndex].clientDiag = append(perUser[userIndex].clientDiag, cd)
			}
			client, laneErr := newLaneAttached(ctx, cfg.Server, u, a, filepath.Join(clientDir, a.ID, u.ID), false, clientTransport, cd, perUser[userIndex].logical, perUser[userIndex].generation, uint64(accountIndex+1))
			if laneErr != nil {
				t.Fatal(laneErr)
			}
			if laneErr = perUser[userIndex].client.attach(client); laneErr != nil {
				t.Fatal(laneErr)
			}
			perUser[userIndex].lanes = append(perUser[userIndex].lanes, client)
			go func(l *lane) { runtimeFailures <- l.run() }(client)
		}
	}
	defer func() {
		for _, runtime := range runtimes {
			runtime.Close()
		}
	}()

	wantReady := accounts * usersN
	for {
		ready := 0
		for i := range perUser {
			for _, l := range perUser[i].lanes {
				if l.ready.Load() {
					ready++
				}
			}
		}
		if ready == wantReady {
			break
		}
		select {
		case laneErr := <-runtimeFailures:
			t.Fatalf("runtime lane failed before readiness: %v", laneErr)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}

	checkRuntime := func(where string) {
		select {
		case laneErr := <-runtimeFailures:
			t.Fatalf("runtime lane failed during %s: %v", where, laneErr)
		default:
		}
	}

	type bulkConnection struct {
		conn   *pipe
		user   int
		stream int
	}
	uploads := make([]bulkConnection, 0, usersN*streams)
	downloads := make([]bulkConnection, 0, usersN*streams)
	probes := make([]*pipe, usersN)
	// Speedtest opens many independent TCP connections concurrently. The old
	// sequential OPEN loop diluted the measured concurrency and could exhaust
	// the setup watchdog before bulk traffic started. Limit simultaneous OPENs
	// to avoid converting the handshake burst into a provider denial test.
	type pair struct {
		upload, download *pipe
		err              error
	}
	connections := make([]pair, usersN*streams)
	var opens sync.WaitGroup
	semaphore := make(chan struct{}, 8)
	for userIndex := range perUser {
		for streamIndex := 0; streamIndex < streams; streamIndex++ {
			userIndex, streamIndex := userIndex, streamIndex
			opens.Add(1)
			go func() {
				defer opens.Done()
				select {
				case semaphore <- struct{}{}:
				case <-ctx.Done():
					connections[userIndex*streams+streamIndex].err = ctx.Err()
					return
				}
				defer func() { <-semaphore }()
				owner := perUser[userIndex].lanes[streamIndex%len(perUser[userIndex].lanes)]
				target := perUser[userIndex].target
				upload, err := owner.open(ctx, target.bulkConnect(true, streamIndex))
				if err != nil {
					connections[userIndex*streams+streamIndex].err = fmt.Errorf("upload OPEN user=%d stream=%d: %w", userIndex+1, streamIndex, err)
					return
				}
				connections[userIndex*streams+streamIndex].upload = upload
				download, err := owner.open(ctx, target.bulkConnect(false, streamIndex))
				if err != nil {
					connections[userIndex*streams+streamIndex].err = fmt.Errorf("download OPEN user=%d stream=%d: %w", userIndex+1, streamIndex, err)
					return
				}
				connections[userIndex*streams+streamIndex].download = download
			}()
		}
	}
	opens.Wait()
	for userIndex := range perUser {
		for streamIndex := 0; streamIndex < streams; streamIndex++ {
			p := connections[userIndex*streams+streamIndex]
			if p.upload != nil {
				uploads = append(uploads, bulkConnection{conn: p.upload, user: userIndex, stream: streamIndex})
			}
			if p.download != nil {
				downloads = append(downloads, bulkConnection{conn: p.download, user: userIndex, stream: streamIndex})
			}
			if p.err != nil {
				for _, old := range uploads {
					_ = old.conn.Close()
				}
				for _, old := range downloads {
					_ = old.conn.Close()
				}
				t.Fatalf("concurrent OPEN failed: %v", p.err)
			}
		}
		probeOwner := perUser[userIndex].lanes[0]
		probe, openErr := probeOwner.open(ctx, perUser[userIndex].target.echoConnect())
		if openErr != nil {
			t.Fatalf("echo OPEN failed: %v", openErr)
		}
		probes[userIndex] = probe
	}

	defer func() {
		cancel()
		for _, item := range uploads {
			_ = item.conn.Close()
		}
		for _, item := range downloads {
			_ = item.conn.Close()
		}
		for _, p := range probes {
			if p != nil {
				_ = p.Close()
			}
		}
		for i := range perUser {
			if perUser[i].target != nil {
				perUser[i].target.close()
			}
		}
	}()
	defer dumpFailure()
	var stop atomic.Bool
	var workers sync.WaitGroup
	workFailures := make(chan error, usersN*streams*4+16)
	for _, item := range uploads {
		item := item
		workers.Add(1)
		go func() {
			defer workers.Done()
			select {
			case <-perUser[item.user].target.startCh:
			case <-ctx.Done():
				return
			}
			generator := newProductionPattern(perUser[item.user].target.streamIdentity(true, item.stream))
			chunk := make([]byte, 32<<10)
			for !stop.Load() {
				generator.fill(chunk)
				if writeErr := productionWriteFull(item.conn, chunk); writeErr != nil {
					if !stop.Load() {
						workFailures <- fmt.Errorf("upload user=%d stream=%d: %w", item.user+1, item.stream, writeErr)
					}
					return
				}
			}
			// The benchmark counts bytes verified by the remote TCP target, so an
			// application-level completion ACK is not part of the measured datapath.
			// On shutdown CloseWrite is driven by the controller as well; do not wait
			// for an ACK through a deliberately draining transport, because that can
			// turn a measured credit stall into a 30-40 s harness teardown stall.
			if closeErr := item.conn.CloseWrite(); closeErr != nil && !stop.Load() {
				workFailures <- closeErr
			}
		}()
	}
	for _, item := range downloads {
		item := item
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := productionReadVerifiedObserver(item.conn, perUser[item.user].target.streamIdentity(false, item.stream), &stop, &perUser[item.user].target.downloadDelivered, func(n int) { perUser[item.user].target.recordWindow(false, n) }); err != nil {
				workFailures <- fmt.Errorf("download user=%d stream=%d: %w", item.user+1, item.stream, err)
			}
		}()
	}

	// All application TCP streams and the echo probe are open before bulk starts.
	// In earlier harness revisions each remote download target started writing as
	// soon as its individual OPEN completed, so setup itself could saturate IMAP
	// and starve later OPENs. Release both directions together instead.
	for i := range perUser {
		perUser[i].target.startBulk()
	}

	// Let grants, batches and APPEND workers reach steady operation before taking
	// the common measurement baseline. This warmup is deliberately outside the
	// reported throughput interval.
	setupTimer.Stop()
	t.Log("production_phase phase=setup event=end")
	phase = "warmup"
	t.Logf("production_phase phase=warmup event=begin deadline_s=%d", warmupSeconds)
	if err := pause(ctx, time.Duration(warmupSeconds)*time.Second); err != nil {
		t.Fatal(err)
	}
	t.Log("production_phase phase=warmup event=end")
	phase = "measurement"
	t.Logf("production_phase phase=measurement event=begin deadline_s=%d", seconds)
	checkRuntime("warmup")
	select {
	case workErr := <-workFailures:
		t.Fatalf("bulk failed during warmup: %v", workErr)
	default:
	}

	providerDone := make(chan struct{})
	var providerStop sync.Once
	stopProvider := func() { providerStop.Do(func() { close(providerDone) }) }
	defer stopProvider()
	if len(providerProfile) > 1 {
		go func() {
			for i, step := range providerProfile {
				providerNetwork.setMbps(step.Mbps)
				t.Logf("provider_cap step=%d mbps=%.3f", i+1, step.Mbps)
				if step.Duration <= 0 {
					return
				}
				timer := time.NewTimer(step.Duration)
				select {
				case <-providerDone:
					if !timer.Stop() {
						<-timer.C
					}
					return
				case <-timer.C:
				}
			}
		}()
	}
	if randomDropMinMS > 0 {
		go func() {
			rng := rand.New(rand.NewSource(faultSeed))
			for {
				span := randomDropMaxMS - randomDropMinMS
				waitMS := randomDropMinMS
				if span > 0 {
					waitMS += rng.Intn(span + 1)
				}
				timer := time.NewTimer(time.Duration(waitMS) * time.Millisecond)
				select {
				case <-providerDone:
					if !timer.Stop() {
						<-timer.C
					}
					return
				case <-timer.C:
				}
				if account, ok := providerNetwork.dropRandom(rng); ok {
					t.Logf("provider_fault kind=line_drop account=%s", account)
				}
			}
		}()
	}
	if accountOutage.Account > 0 {
		go func() {
			timer := time.NewTimer(accountOutage.Start)
			select {
			case <-providerDone:
				if !timer.Stop() {
					<-timer.C
				}
				return
			case <-timer.C:
			}
			account := fmt.Sprintf("a%02d", accountOutage.Account)
			dropped := providerNetwork.outageAccount(account, accountOutage.Duration)
			t.Logf("provider_fault kind=account_outage account=%s duration_ms=%d closed_connections=%d", account, accountOutage.Duration.Milliseconds(), dropped)
		}()
	}
	targets := make([]*productionLoadTarget, usersN)
	probeConns := make([]net.Conn, usersN)
	for i := range perUser {
		targets[i] = perUser[i].target
		probeConns[i] = probes[i]
	}

	// Snapshotting uses a separate worker and never holds the fixed byte/probe
	// sampler clock. One correlated record per second, bounded to 128 records.
	debtCtx, stopDebt := context.WithCancel(ctx)
	debtDone := make(chan struct{})
	go func() {
		defer close(debtDone)
		if !diagnosticsEnabled {
			return
		}
		started := time.Now()
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for index := 1; index <= 128; index++ {
			select {
			case <-debtCtx.Done():
				return
			case <-tick.C:
			}
			var lanes []*lane
			for _, u := range perUser {
				lanes = append(lanes, u.lanes...)
				u.server.mu.Lock()
				for _, g := range u.server.groups {
					g.mu.Lock()
					for _, l := range g.lanes {
						lanes = append(lanes, l)
					}
					g.mu.Unlock()
				}
				u.server.mu.Unlock()
			}
			snapshots := make([]laneProgress, len(lanes))
			var wg sync.WaitGroup
			snapshotCtx, done := context.WithTimeout(debtCtx, 200*time.Millisecond)
			for i, l := range lanes {
				wg.Add(1)
				go func(i int, l *lane) { defer wg.Done(); snapshots[i] = l.progressSnapshot(snapshotCtx) }(i, l)
			}
			wg.Wait()
			done()
			sort.Slice(snapshots, func(i, j int) bool {
				if snapshots[i].User != snapshots[j].User {
					return snapshots[i].User < snapshots[j].User
				}
				if snapshots[i].Server != snapshots[j].Server {
					return !snapshots[i].Server
				}
				return snapshots[i].Account < snapshots[j].Account
			})
			raw, _ := json.Marshal(snapshots)
			t.Logf("production_debt index=%d observed_ms=%d lanes=%s", index, time.Since(started).Milliseconds(), raw)
		}
	}()
	defer func() { stopDebt(); <-debtDone }()
	var overlapByUser []int
	var uploadMbps, downloadMbps float64
	var maxRTT time.Duration
	for repeat := 1; repeat <= repeats; repeat++ {
		t.Logf("rework_run index=%d total=%d shared_session=true", repeat, repeats)
		measurement, measureErr := productionMeasure(ctx, productionMeasureOptions{Duration: measurementTarget, Cadence: time.Second, ProbeDeadline: probeDeadline, MaxGap: maxGap}, targets, probeConns, runtimeFailures, workFailures, t.Logf)
		elapsed := measurement.Elapsed
		rttSamples, overlapSamples, timeouts, missed := productionSampleSummary(measurement.Samples, -1)
		overlapByUser = make([]int, usersN)
		var aggregateUpload, aggregateDownload int64
		for i := range perUser {
			aggregateUpload += measurement.Upload[i]
			aggregateDownload += measurement.Download[i]
			samples, overlap, userTimeouts, userMissed := productionSampleSummary(measurement.Samples, i)
			overlapByUser[i] = overlap
			expected := int((min(elapsed, measurementTarget) + time.Second - 1) / time.Second)
			received := 0
			for _, sample := range measurement.Samples {
				if sample.User == i {
					received++
				}
			}
			unobserved := max(0, expected-received)
			t.Logf("production_user user=%d upload_delivered_bytes=%d download_delivered_bytes=%d upload_mbps=%.3f download_mbps=%.3f rtt_samples=%d rtt_p50_ms=%d rtt_p95_ms=%d rtt_p99_ms=%d rtt_max_ms=%d timeouts=%d missed=%d pending_or_unobserved=%d", i+1, measurement.Upload[i], measurement.Download[i], float64(measurement.Upload[i]*8)/elapsed.Seconds()/1e6, float64(measurement.Download[i]*8)/elapsed.Seconds()/1e6, len(samples), productionPercentile(samples, .5).Milliseconds(), productionPercentile(samples, .95).Milliseconds(), productionPercentile(samples, .99).Milliseconds(), productionPercentile(samples, 1).Milliseconds(), userTimeouts, userMissed, unobserved)
		}
		uploadMbps = float64(aggregateUpload*8) / elapsed.Seconds() / 1e6
		downloadMbps = float64(aggregateDownload*8) / elapsed.Seconds() / 1e6
		maxRTT = productionPercentile(rttSamples, 1)
		t.Logf("production_result phase=measurement_end accounts=%d users=%d streams_per_direction=%d command_delay_ms=%d warmup_s=%d measured_ms=%d upload_mbps=%.3f download_mbps=%.3f rtt_samples=%d rtt_p50_ms=%d rtt_p95_ms=%d rtt_p99_ms=%d rtt_max_ms=%d timeouts=%d missed=%d overlap_samples=%d diagnostics=%t trace=%t quantile=nearest_rank failure=%v", accounts, usersN, streams, commandDelayMS, warmupSeconds, elapsed.Milliseconds(), uploadMbps, downloadMbps, len(rttSamples), productionPercentile(rttSamples, .5).Milliseconds(), productionPercentile(rttSamples, .95).Milliseconds(), productionPercentile(rttSamples, .99).Milliseconds(), maxRTT.Milliseconds(), timeouts, missed, overlapSamples, diagnosticsEnabled, traceEnabled, measureErr)
		t.Log("production_phase phase=measurement event=end")
		if measureErr != nil {
			for _, event := range measurement.Windows {
				t.Logf("failure_timeline %s", event)
			}
			t.Fatal(measureErr)
		}
		for _, sample := range measurement.Samples {
			if sample.Err != nil {
				t.Fatalf("probe failed at measurement boundary: %v", sample.Err)
			}
		}
		if len(rttSamples) == 0 {
			t.Fatal("no successful loaded probe")
		}
		if os.Getenv("ITOM_SPEEDTEST_VALIDATE") == "1" {
			if err := speedtestWindowQualification(measurement.Bandwidth, elapsed, usersN, speedtestAcceptedMbps); err != nil {
				// Development-only evidence on a failing gate: the existing fatal
				// branch bypasses post-load diagnostics, hiding the physical mailbox
				// bottleneck. Capture counter snapshots only AFTER the measured window,
				// without weakening throughput/RTT acceptance or leaking MIME contents.
				if diagnosticsEnabled && os.Getenv("ITOM_SPEEDTEST_DIAG_ON_FAIL") == "1" {
					for userIndex := range perUser {
						productionLogDiagnostics(t, "client", userIndex+1, perUser[userIndex].clientDiag)
						productionLogDiagnostics(t, "server", userIndex+1, perUser[userIndex].serverDiag)
					}
					providerSnapshot := providerNetwork.snapshot()
					t.Logf("speedtest_failure_provider cap_mbps=%.3f active_connections=%d accepted=%d dropped=%d", providerSnapshot.Mbps, providerSnapshot.Active, providerSnapshot.Accepted, providerSnapshot.Dropped)
				}
				t.Fatalf("speedtest throughput gate: %v", err)
			}
			if err := speedtestRTTQualification(rttSamples, timeouts, missed, overlapSamples); err != nil {
				t.Fatalf("speedtest loaded RTT gate: %v", err)
			}
			// Require full-window useful TCP goodput, not only a fast tail.
			if uploadMbps < speedtestAcceptedMbps || downloadMbps < speedtestAcceptedMbps {
				t.Fatalf("speedtest full-window upload %.3f download %.3f Mbps below %.1f", uploadMbps, downloadMbps, speedtestAcceptedMbps)
			}
		}

	}
	stopProvider()
	stopDebt()
	<-debtDone
	phase = "drain"
	t.Log("production_phase phase=drain event=begin deadline_s=10")

	stop.Store(true)
	for i := range perUser {
		perUser[i].target.stopped.Store(true)
	}
	// Half-close uploads from the controller as well, so a writer blocked in the
	// logical pipe cannot keep shutdown waiting for fresh WINDOW credit. The read
	// side remains available for the optional byte-count acknowledgement.
	for _, item := range uploads {
		_ = item.conn.CloseWrite()
	}
	for _, item := range downloads {
		_ = item.conn.Close()
	}
	workersDone := make(chan struct{})
	go func() {
		workers.Wait()
		close(workersDone)
	}()
	select {
	case <-workersDone:
	case <-time.After(10 * time.Second):
		t.Fatal("drain deadline exceeded: bulk workers did not stop")
	}
	for _, item := range uploads {
		_ = item.conn.Close()
	}
	for _, probe := range probes {
		_ = probe.Close()
	}
	for i := range perUser {
		if targetErr := perUser[i].target.firstError(); targetErr != nil {
			t.Fatalf("target user=%d: %v", i+1, targetErr)
		}
	}
	select {
	case workErr := <-workFailures:
		t.Fatalf("bulk completion failed: %v", workErr)
	default:
	}
	checkRuntime("post_load")

	t.Log("production_phase phase=drain event=end")
	missingOverlapUsers := []int{}
	for i, n := range overlapByUser {
		if n == 0 {
			missingOverlapUsers = append(missingOverlapUsers, i+1)
		}
	}

	providerSnapshot := providerNetwork.snapshot()
	t.Logf("provider_network cap_mbps=%.3f active_connections=%d down_accounts=%d accepted=%d dropped=%d uplink_tls_bytes=%d downlink_tls_bytes=%d fault_seed=%d random_drop_min_ms=%d random_drop_max_ms=%d", providerSnapshot.Mbps, providerSnapshot.Active, providerSnapshot.DownAccounts, providerSnapshot.Accepted, providerSnapshot.Dropped, providerSnapshot.UplinkBytes, providerSnapshot.DownlinkBytes, faultSeed, randomDropMinMS, randomDropMaxMS)
	for accountIndex, runtime := range runtimes {
		s := runtime.snapshot()
		t.Logf("account_runtime account=%d live_workers=%d busy_workers=%d queued_requests=%d pending_bytes=%d completed=%d failed=%d served_mime_bytes=%d max_queue_wait_ms=%d max_service_ms=%d user_completed=%v user_mime_bytes=%v", accountIndex+1, s.LiveWorkers, s.BusyWorkers, s.QueuedRequests, s.PendingBytes, s.Completed, s.Failed, s.ServedBytes, s.MaxQueueWaitMS, s.MaxServiceMS, s.UserCompleted, s.UserBytes)
	}
	if diagnosticsEnabled {
		for userIndex := range perUser {
			productionLogDiagnostics(t, "client", userIndex+1, perUser[userIndex].clientDiag)
			productionLogDiagnostics(t, "server", userIndex+1, perUser[userIndex].serverDiag)
		}
	}

	if len(missingOverlapUsers) > 0 {
		t.Fatalf("users %v have no RTT probe proven to overlap active upload/download delivery", missingOverlapUsers)
	}

	if minMbps > 0 && (uploadMbps < minMbps || downloadMbps < minMbps) {
		t.Fatalf("production throughput qualification failed: upload %.3f Mbps download %.3f Mbps < %.3f Mbps", uploadMbps, downloadMbps, minMbps)
	}
	if maxRTTMS > 0 && float64(maxRTT.Milliseconds()) >= maxRTTMS {
		t.Fatalf("production RTT qualification failed: max %d ms >= %.0f ms", maxRTT.Milliseconds(), maxRTTMS)
	}

	phase = "cancellation"
	t.Log("production_phase phase=cancellation event=begin deadline_s=10")
	cancel()
	stopTimer := time.NewTimer(10 * time.Second)
	defer stopTimer.Stop()
	wantStops := accounts * usersN * 2
	for i := 0; i < wantStops; i++ {
		select {
		case <-runtimeFailures:
		case <-stopTimer.C:
			t.Fatal("production lane worker did not stop")
		}
	}
	t.Log("production_phase phase=cancellation event=end")
}

func loadEnvInt(t *testing.T, name string, def, minValue, maxValue int) int {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minValue || value > maxValue {
		t.Fatalf("%s must be an integer in [%d,%d]", name, minValue, maxValue)
	}
	return value
}

func loadEnvFloat(t *testing.T, name string, def float64) float64 {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		t.Fatalf("%s must be a non-negative number", name)
	}
	return value
}

type productionLoadTarget struct {
	chunkBytes                         int
	chunkPause                         time.Duration
	measurement                        atomic.Pointer[applicationByteClock]
	test                               *testing.T
	identity                           string
	echoListener                       net.Listener
	startCh                            chan struct{}
	startOnce                          sync.Once
	uploadDelivered, downloadDelivered atomic.Int64
	stopped                            atomic.Bool
	errors                             chan error
	mu                                 sync.Mutex
	connections                        []net.Conn
}

func newProductionLoadTarget(t *testing.T, identity string) *productionLoadTarget {
	target := &productionLoadTarget{test: t, identity: identity, startCh: make(chan struct{}), errors: make(chan error, 256)}
	target.chunkBytes = loadEnvInt(t, "ITOM_LOAD_CHUNK_BYTES", 32<<10, 512, 64<<10)
	target.chunkPause = time.Duration(loadEnvInt(t, "ITOM_LOAD_CHUNK_PAUSE_MS", 0, 0, 100)) * time.Millisecond
	target.echoListener = target.listen()
	t.Cleanup(target.close)
	go target.runEcho()
	return target
}
func (t *productionLoadTarget) listen() net.Listener {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.test.Fatal(err)
	}
	t.test.Cleanup(func() { _ = l.Close() })
	return l
}
func (t *productionLoadTarget) close() {
	t.stopped.Store(true)
	t.startBulk()
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, c := range t.connections {
		_ = c.Close()
	}
}
func (t *productionLoadTarget) track(c net.Conn) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped.Load() {
		_ = c.Close()
		return
	}
	t.connections = append(t.connections, c)
}
func (t *productionLoadTarget) startBulk() { t.startOnce.Do(func() { close(t.startCh) }) }
func (t *productionLoadTarget) connect(l net.Listener) wire.Connect {
	_, port, _ := net.SplitHostPort(l.Addr().String())
	n, _ := strconv.Atoi(port)
	return wire.Connect{Host: "127.0.0.1", Port: uint16(n)}
}
func (t *productionLoadTarget) echoConnect() wire.Connect { return t.connect(t.echoListener) }
func (t *productionLoadTarget) streamIdentity(upload bool, stream int) string {
	direction := "download"
	if upload {
		direction = "upload"
	}
	return fmt.Sprintf("%s/%s/%d", t.identity, direction, stream)
}

// Each target port has an independently known expected stream identity. A
// swapped entire stream (including any self-declared header) cannot validate.
func (t *productionLoadTarget) bulkConnect(upload bool, stream int) wire.Connect {
	l := t.listen()
	identity := t.streamIdentity(upload, stream)
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		t.track(c)
		// A reconnect must preserve this target TCP stream. Observe any second
		// dial to the stream's dedicated port instead of leaving it in backlog.
		go func() {
			for {
				duplicate, err := l.Accept()
				if err != nil {
					return
				}
				duplicate.Close()
				if !t.stopped.Load() {
					t.report(fmt.Errorf("%s: duplicate target TCP dial", identity))
				}
			}
		}()
		generator := newProductionPattern(identity)
		if upload {
			buf := make([]byte, 64<<10)
			expected := make([]byte, len(buf))
			for {
				n, err := c.Read(buf)
				if n > 0 {
					if e := generator.verify(buf[:n], expected); e != nil {
						t.report(fmt.Errorf("%s: %w", identity, e))
						return
					}
					t.uploadDelivered.Add(int64(n))
					t.recordWindow(true, n)
				}
				if err != nil {
					if !t.stopped.Load() {
						t.report(fmt.Errorf("%s: unexpected active-stream termination: %w", identity, err))
					}
					return
				}
			}
		}
		<-t.startCh
		chunk := make([]byte, t.chunkBytes)
		for !t.stopped.Load() {
			if t.chunkPause > 0 {
				time.Sleep(t.chunkPause)
			}
			generator.fill(chunk)
			if err := productionWriteFull(c, chunk); err != nil {
				if !t.stopped.Load() {
					t.report(fmt.Errorf("%s: target write: %w", identity, err))
				}
				return
			}
		}
	}()
	return t.connect(l)
}
func (t *productionLoadTarget) report(err error) {
	if err == nil {
		return
	}
	select {
	case t.errors <- err:
	default:
	}
}
func (t *productionLoadTarget) firstError() error {
	select {
	case err := <-t.errors:
		return err
	default:
		return nil
	}
}
func (t *productionLoadTarget) runEcho() {
	for {
		c, err := t.echoListener.Accept()
		if err != nil {
			return
		}
		t.track(c)
		go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
	}
}

func productionWriteFull(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if n > 0 {
			data = data[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	return nil
}

type productionPattern struct {
	state uint64
	word  [8]byte
	used  int
}

func newProductionPattern(identity string) *productionPattern {
	digest := sha256.Sum256([]byte(identity))
	seed := binary.LittleEndian.Uint64(digest[:8])
	if seed == 0 {
		seed = 0x9e3779b97f4a7c15
	}
	return &productionPattern{state: seed, used: 8}
}

func (p *productionPattern) refill() {
	x := p.state
	x ^= x >> 12
	x ^= x << 25
	x ^= x >> 27
	p.state = x
	binary.LittleEndian.PutUint64(p.word[:], x*0x2545f4914f6cdd1d)
	p.used = 0
}

func (p *productionPattern) fill(dst []byte) {
	for len(dst) > 0 {
		if p.used == len(p.word) {
			p.refill()
		}
		n := copy(dst, p.word[p.used:])
		p.used += n
		dst = dst[n:]
	}
}

func (p *productionPattern) verify(data, scratch []byte) error {
	if len(scratch) < len(data) {
		return fmt.Errorf("verification scratch too small")
	}
	expected := scratch[:len(data)]
	p.fill(expected)
	if !bytes.Equal(data, expected) {
		return fmt.Errorf("content mismatch")
	}
	return nil
}

func productionPercentile(samples []time.Duration, q float64) time.Duration {
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	if len(sorted) == 0 {
		return 0
	}
	index := int(math.Ceil(float64(len(sorted))*q)) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func productionLogDiagnostics(t *testing.T, side string, user int, diags []*diagnostic) {
	t.Helper()
	var appendCount, appendBytes, controlAppendCount, controlAppendBytes, dataAppendCount, dataAppendBytes int64
	var dataAttempts, dataRecords, dataPayloadBytes, dataBatchCount, dataBatchRecords, dataBatchBodyBytes int64
	var maxDataWait, maxLocalGrantWait, maxRemoteGrantWait, localGrantHeadroomWaits, localGrantLimitWaits, maxPumpWait, maxStripeWait, maxGrantWait, maxCleanup, maxControlWait int64
	var peakDataHeldBytes, peakDataHeldMessages uint64
	var windowSent, windowReceived, receiptSent, receiptReceived int64
	var maxWindowSent, maxWindowReceived uint64
	connections := 0
	for _, d := range diags {
		s := d.snapshot()
		appendCount += s.AppendCount
		appendBytes += s.AppendBytes
		controlAppendCount += s.ControlAppendCount
		controlAppendBytes += s.ControlAppendBytes
		dataAppendCount += s.DataAppendCount
		dataAppendBytes += s.DataAppendBytes
		dataAttempts += s.DataAttempts
		dataRecords += s.DataRecords
		dataPayloadBytes += s.DataPayloadBytes
		dataBatchCount += s.DataBatchCount
		dataBatchRecords += s.DataBatchRecords
		dataBatchBodyBytes += s.DataBatchBodyBytes
		maxDataWait = max(maxDataWait, s.MaxDataWaitMS)
		maxLocalGrantWait = max(maxLocalGrantWait, s.MaxLocalGrantWaitMS)
		maxRemoteGrantWait = max(maxRemoteGrantWait, s.MaxRemoteGrantWaitMS)
		localGrantHeadroomWaits += s.LocalGrantHeadroomWaits
		localGrantLimitWaits += s.LocalGrantLimitWaits
		peakDataHeldBytes = max(peakDataHeldBytes, s.PeakDataHeldBytes)
		peakDataHeldMessages = max(peakDataHeldMessages, s.PeakDataHeldMessages)
		maxPumpWait = max(maxPumpWait, s.MaxPumpQueueWaitMS)
		maxStripeWait = max(maxStripeWait, s.MaxStripeWaitMS)
		maxGrantWait = max(maxGrantWait, s.MaxGrantSelectWaitMS)
		maxCleanup = max(maxCleanup, s.CleanupMS)
		maxControlWait = max(maxControlWait, s.MaxControlWaitMS)
		windowSent += s.WindowSent
		windowReceived += s.WindowReceived
		receiptSent += s.ReceiptSent
		receiptReceived += s.ReceiptReceived
		maxWindowSent = max(maxWindowSent, s.MaxWindowSent)
		maxWindowReceived = max(maxWindowReceived, s.MaxWindowReceived)
		connections += s.IOConnections
	}
	avgDataMIME := 0.0
	if dataAppendCount > 0 {
		avgDataMIME = float64(dataAppendBytes) / float64(dataAppendCount)
	}
	avgBatchRecords := 0.0
	if dataBatchCount > 0 {
		avgBatchRecords = float64(dataBatchRecords) / float64(dataBatchCount)
	}
	mimeRatio := 0.0
	if dataPayloadBytes > 0 {
		mimeRatio = float64(dataAppendBytes) / float64(dataPayloadBytes)
	}
	t.Logf("production_diag side=%s user=%d io_connections=%d append_count=%d append_bytes=%d control_append_count=%d control_append_bytes=%d data_append_count=%d data_append_bytes=%d avg_data_mime_bytes=%.0f data_attempts=%d data_records=%d data_payload_bytes=%d data_batch_count=%d data_batch_records=%d avg_batch_records=%.2f data_batch_body_bytes=%d mime_to_payload_ratio=%.3f max_data_wait_ms=%d max_local_grant_wait_ms=%d max_remote_grant_wait_ms=%d local_grant_headroom_waits=%d local_grant_limit_waits=%d peak_data_held_bytes=%d peak_data_held_messages=%d max_pump_queue_wait_ms=%d max_stripe_wait_ms=%d max_grant_select_wait_ms=%d max_control_wait_ms=%d max_cleanup_ms=%d window_sent=%d window_received=%d receipt_sent=%d receipt_received=%d max_window_sent=%d max_window_received=%d", side, user, connections, appendCount, appendBytes, controlAppendCount, controlAppendBytes, dataAppendCount, dataAppendBytes, avgDataMIME, dataAttempts, dataRecords, dataPayloadBytes, dataBatchCount, dataBatchRecords, avgBatchRecords, dataBatchBodyBytes, mimeRatio, maxDataWait, maxLocalGrantWait, maxRemoteGrantWait, localGrantHeadroomWaits, localGrantLimitWaits, peakDataHeldBytes, peakDataHeldMessages, maxPumpWait, maxStripeWait, maxGrantWait, maxControlWait, maxCleanup, windowSent, windowReceived, receiptSent, receiptReceived, maxWindowSent, maxWindowReceived)
	for accountIndex, d := range diags {
		s := d.snapshot()
		t.Logf("production_lane side=%s user=%d account=%d stage=%s queue=%d outbox=%d data_held_bytes=%d data_held_messages=%d remote_gc=%d obsolete=%d known=%d available_grants=%d issued_grants=%d pending_descriptors=%d received_records=%d cleanup_remote_runs=%d cleanup_remote_messages=%d cleanup_local_data_runs=%d cleanup_local_data_messages=%d cleanup_local_control_runs=%d cleanup_local_control_messages=%d cleanup_presence_runs=%d max_control_wait_ms=%d window_sent=%d window_received=%d receipt_sent=%d receipt_received=%d max_window_sent=%d max_window_received=%d", side, user, accountIndex+1, s.Stage, s.Queue, s.Outbox, s.DataBytes, s.DataMessages, s.RemoteGC, s.Obsolete, s.Known, s.AvailableGrants, s.IssuedGrants, s.PendingDescriptors, s.ReceivedRecords, s.CleanupRemoteRuns, s.CleanupRemoteMessages, s.CleanupLocalDataRuns, s.CleanupLocalDataMessages, s.CleanupLocalControlRuns, s.CleanupLocalControlMessages, s.CleanupPresenceRuns, s.MaxControlWaitMS, s.WindowSent, s.WindowReceived, s.ReceiptSent, s.ReceiptReceived, s.MaxWindowSent, s.MaxWindowReceived)
	}
}
