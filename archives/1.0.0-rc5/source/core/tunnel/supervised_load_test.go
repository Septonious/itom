package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
	"net"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Synthetic diagnostics use actual StartClient/Serve supervisors and local TLS/IMAP.
// These are not validated against the field provider and cannot qualify a release.
// Half outages include stream owners; the same application sockets and verified
// byte generators remain active across outage, warmup and measurement phases.
func testSupervisedProductionLoad(t *testing.T) {
	t.Log("synthetic_measurement=true field_qualified=false")
	if raceInstrumented {
		t.Fatal("production performance qualification requires a non-race test binary; run supervisor recovery tests separately with -race")
	}
	n := loadEnvInt(t, "ITOM_LOAD_ACCOUNTS", 10, 2, 128)
	streams := loadEnvInt(t, "ITOM_LOAD_STREAMS", 4, 1, 32)
	seconds := loadEnvInt(t, "ITOM_LOAD_SECONDS", 60, 5, 7200)
	warmup := loadEnvInt(t, "ITOM_LOAD_WARMUP_SECONDS", 10, 1, 30)
	loss := loadEnvFloat(t, "ITOM_LOAD_MOBILE_LOSS_PERCENT", 0)
	probeDefault := 1000
	if os.Getenv("ITOM_LOAD_MOBILE") == "1" && loss > 0 {
		probeDefault = 3000
	}
	probeCadence := time.Duration(loadEnvInt(t, "ITOM_LOAD_PROBE_CADENCE_MS", probeDefault, 1000, 3000)) * time.Millisecond
	probeDeadline := time.Duration(loadEnvInt(t, "ITOM_LOAD_PROBE_DEADLINE_MS", probeDefault, 1000, 3000)) * time.Millisecond
	if probeDeadline > probeCadence || time.Duration(seconds)*time.Second%probeCadence != 0 {
		t.Fatal("probe deadline/cadence must preserve a complete fixed schedule")
	}
	expectedProbes := int(time.Duration(seconds) * time.Second / probeCadence)
	rttPriority := os.Getenv("ITOM_LOAD_RTT_PRIORITY") == "1"
	goal := loadEnvFloat(t, "ITOM_LOAD_GOAL_MBPS", 0)
	if goal != 0 && goal != 100 && goal != 125 {
		t.Fatal("ITOM_LOAD_GOAL_MBPS must be 0, the temporary target 100, or the historical target 125")
	}
	t.Logf("supervised_probe_schedule cadence_ms=%d deadline_ms=%d expected=%d loss_scoped=%t bandwidth_cadence_ms=1000 hard_deadline=true", probeCadence.Milliseconds(), probeDeadline.Milliseconds(), expectedProbes, os.Getenv("ITOM_LOAD_MOBILE") == "1" && loss > 0)
	if loadEnvInt(t, "ITOM_LOAD_USERS", 1, 1, 2) != 1 {
		t.Fatal("supervised qualification currently supports one user")
	}
	availability := os.Getenv("ITOM_LOAD_AVAILABILITY")
	if availability == "" {
		availability = "all"
	}
	if availability != "all" && availability != "half-owner" && availability != "half-sibling" && availability != "rotate-half" && availability != "half-recover" {
		t.Fatal("unknown availability case", availability)
	}
	if n%2 != 0 && (availability == "half-owner" || availability == "half-sibling" || availability == "rotate-half" || availability == "half-recover") {
		t.Fatal("half qualification requires even account count")
	}
	var retained func(string, string, servercfg.Config, servercfg.User)
	if os.Getenv("ITOM_LOAD_RETAINED_STATE") == "1" {
		retained = rev4RetainedState(t)
	}
	f := newProductionSupervisorFixtureState(t, n, retained)
	logical, generation := f.client.logicalSession, f.client.generation
	target := newProductionLoadTarget(t, fmt.Sprintf("%x/supervised", logical))
	f.network.setMbps(loadEnvFloat(t, "ITOM_LOAD_PROVIDER_MBPS", 0))
	profiles := parseProviderProfile(t, os.Getenv("ITOM_LOAD_PROVIDER_PROFILE"), loadEnvFloat(t, "ITOM_LOAD_PROVIDER_MBPS", 0))
	mobile := os.Getenv("ITOM_LOAD_MOBILE") == "1"
	mobileCap := loadEnvFloat(t, "ITOM_LOAD_MOBILE_MBPS", 600)
	var baseline time.Duration
	var profile mobileLinkProfile
	if mobile {
		profile = mobileLinkProfile{RTT: time.Duration(loadEnvInt(t, "ITOM_LOAD_MOBILE_RTT_MS", 200, 100, 300)) * time.Millisecond, Mbps: mobileCap, LossPercent: loadEnvFloat(t, "ITOM_LOAD_MOBILE_LOSS_PERCENT", 0), Seed: int64(loadEnvInt(t, "ITOM_LOAD_FAULT_SEED", 1, 0, 1<<30))}
		if !f.network.mobile.set(profile) {
			t.Fatal("invalid mobile profile")
		}
	}
	openApplication := func(target wire.Connect) (*pipe, error) {
		c, _, err := f.client.Open(f.ctx, target)
		if err != nil {
			return nil, err
		}
		p, ok := c.(*pipe)
		if !ok {
			c.Close()
			return nil, fmt.Errorf("unexpected application connection %T", c)
		}
		return p, nil
	}
	t.Log("application_open_path=Client.Open manually_assigned_owners=false mobile_profile_active_before_application_open=true")
	var sockets []*pipe
	var workers sync.WaitGroup
	var stop atomic.Bool
	failures := make(chan error, 2*streams+4)
	for i := 0; i < streams; i++ {
		up, err := openApplication(target.bulkConnect(true, i))
		if err != nil {
			t.Fatal(err)
		}
		down, err := openApplication(target.bulkConnect(false, i))
		if err != nil {
			t.Fatal(err)
		}
		sockets = append(sockets, up, down)
		workers.Add(2)
		go func(p *pipe, index int) {
			defer workers.Done()
			<-target.startCh
			pattern := newProductionPattern(target.streamIdentity(true, index))
			buf := make([]byte, target.chunkBytes)
			for !stop.Load() {
				if target.chunkPause > 0 {
					time.Sleep(target.chunkPause)
				}
				pattern.fill(buf)
				if err := productionWriteFull(p, buf); err != nil {
					if !stop.Load() {
						failures <- err
					}
					return
				}
			}
		}(up, i)
		go func(p *pipe, index int) {
			defer workers.Done()
			if err := productionReadVerifiedObserver(p, target.streamIdentity(false, index), &stop, &target.downloadDelivered, func(n int) { target.recordWindow(false, n) }); err != nil {
				if !stop.Load() {
					failures <- err
				}
			}
		}(down, i)
	}
	probe, err := openApplication(target.echoConnect())
	if err != nil {
		t.Fatal(err)
	}
	sockets = append(sockets, probe)
	defer func() {
		stop.Store(true)
		target.close()
		for _, p := range sockets {
			p.Close()
		}
		workers.Wait()
	}()
	if mobile {
		c, e := f.baselineDial(f.ctx)
		if e != nil {
			t.Fatal("baseline dial", e)
		}
		var samples []time.Duration
		for i := 0; i < 5; i++ {
			ctx, cancel := context.WithTimeout(f.ctx, 2*time.Second)
			rtt, e := c.Ping(ctx)
			cancel()
			if e != nil {
				c.Close()
				t.Fatal("baseline NOOP", e)
			}
			samples = append(samples, rtt)
			t.Logf("mobile_baseline_sample index=%d rtt_ns=%d", i+1, rtt.Nanoseconds())
		}
		c.Close()
		baseline = productionPercentile(samples, .5)
		t.Logf("mobile_baseline method=authenticated_NOOP unloaded=true median_ns=%d configured_rtt_ms=%d cap_mbps=%.3f loss_percent=%.3f seed=%d", baseline.Nanoseconds(), profile.RTT.Milliseconds(), profile.Mbps, profile.LossPercent, profile.Seed)
	}
	coldBegin := time.Now()
	coldClock := newApplicationByteClock(coldBegin, time.Duration(warmup+10)*time.Second, time.Second)
	target.measurement.Store(coldClock)
	target.startBulk()
	snapshotCtx, stopSnapshots := context.WithCancel(f.ctx)
	var snapshotsWG sync.WaitGroup
	if os.Getenv("ITOM_LOAD_DIAGNOSTICS") == "1" || os.Getenv("ITOM_LOAD_SNAPSHOTS") == "1" {
		snapshotsWG.Add(1)
		go func() { defer snapshotsWG.Done(); supervisedSnapshots(snapshotCtx, t, f) }()
	}
	defer func() { stopSnapshots(); snapshotsWG.Wait() }()
	t.Logf("supervised_config accounts=%d minimum_working=%d streams=%d availability=%s delay_ms=%d measurement_s=%d warmup_s=%d", n, (n+1)/2, streams, availability, loadEnvInt(t, "ITOM_LOAD_COMMAND_DELAY_MS", 80, 0, 5000), seconds, warmup)
	rounds := loadEnvInt(t, "ITOM_LOAD_REPEATS", 1, 1, 3)
	if availability == "rotate-half" || availability == "half-recover" {
		rounds = 2
	}
	for round := 0; round < rounds; round++ {
		working := n
		if availability != "all" {
			if round > 0 {
				f.network.mu.Lock()
				clear(f.network.downTill)
				f.network.mu.Unlock()
				for i := 1; i <= n; i++ {
					f.waitLane(t, fmt.Sprintf("a%02d", i), 0)
				}
			}
			working = n / 2
			start := 1
			if availability == "half-sibling" || round > 0 {
				start = n/2 + 1
			}
			dropCount := n / 2
			if availability == "half-recover" && round > 0 {
				working, dropCount = n, 0
			}
			for i := start; i < start+dropCount; i++ {
				f.outageAccount(fmt.Sprintf("a%02d", i), 4*time.Minute)
			}
			t.Logf("supervised_fault round=%d working_accounts=%d dropped_range=%d..%d", round+1, working, start, start+dropCount-1)
		}
		// Bounded recovery is measured separately, never folded into steady-state RTT.
		recoveryStart := time.Now()
		_ = probe.SetDeadline(time.Now().Add(8 * time.Second))
		payload := []byte(fmt.Sprintf("recovery/%d", round))
		got := make([]byte, len(payload))
		if err := productionWriteFull(probe, payload); err != nil {
			t.Fatal("same-socket recovery write", err)
		}
		if _, err := readExactProbe(probe, got, payload); err != nil {
			t.Fatal("same-socket recovery read", err)
		}
		t.Logf("supervised_recovery round=%d rtt_ms=%d", round+1, time.Since(recoveryStart).Milliseconds())
		if round == 0 && os.Getenv("ITOM_LOAD_STARTUP_PROBES") == "1" {
			startup, e := productionMeasure(f.ctx, productionMeasureOptions{Duration: time.Duration(warmup) * time.Second, Cadence: time.Second, BandwidthCadence: time.Second, ProbeDeadline: time.Second, MaxGap: 5 * time.Second}, []*productionLoadTarget{target}, []net.Conn{probe}, f.carrierExits.failures, failures, t.Logf)
			rtts, overlap, timeouts, missed := productionSampleSummary(startup.Samples, 0)
			t.Logf("supervised_cold_probes elapsed_ms=%d samples=%d overlap=%d max_ms=%d timeouts=%d missed=%d failure=%v", startup.Elapsed.Milliseconds(), len(rtts), overlap, productionPercentile(rtts, 1).Milliseconds(), timeouts, missed, e)
			if e != nil || timeouts != 0 || missed != 0 || len(rtts) != warmup {
				t.Fatal("cold startup failed", e, "samples", len(rtts), "timeouts", timeouts, "missed", missed)
			}
			if os.Getenv("ITOM_LOAD_COLD_ONLY") == "1" {
				return
			}
		} else if err := pause(f.ctx, time.Duration(warmup)*time.Second); err != nil {
			t.Fatal(err)
		}
		if round == 0 {
			if os.Getenv("ITOM_LOAD_STARTUP_PROBES") != "1" {
				logColdRamp(t, coldClock, int(time.Since(coldBegin)/time.Second))
			}
		}
		select {
		case err := <-failures:
			t.Fatal("bulk lost during recovery/warmup", err)
		default:
		}
		if f.client.logicalSession != logical || f.client.generation != generation || f.client.Stats().TCP != len(sockets) {
			t.Fatal("logical/socket identity changed after half outage")
		}
		profileCtx, cancelProfile := context.WithCancel(f.ctx)
		var profileWG sync.WaitGroup
		if len(profiles) > 1 {
			profileWG.Add(1)
			go func() {
				defer profileWG.Done()
				for _, step := range profiles {
					f.network.setMbps(step.Mbps)
					t.Logf("supervised_cap mbps=%.3f", step.Mbps)
					if step.Duration <= 0 {
						return
					}
					if pause(profileCtx, step.Duration) != nil {
						return
					}
				}
			}()
		}
		roundCadence, roundDeadline := probeCadence, probeDeadline
		roundExpected := int((time.Duration(seconds)*time.Second + roundCadence - 1) / roundCadence)
		// Center probes in the load periods. A boundary-at-zero probe had only
		// its short RTT to observe a completed BODY, while later probes saw the
		// preceding full cadence. Keep every probe and the complete byte clock.
		t.Logf("supervised_round_schedule round=%d cadence_ms=%d phase_ms=%d deadline_ms=%d expected=%d survival=%t bandwidth_clock_ms=1000", round+1, roundCadence.Milliseconds(), (roundCadence / 2).Milliseconds(), roundDeadline.Milliseconds(), roundExpected, false)
		carriers := snapshotLoadCarriers(t, f, n, working)
		result, measureErr := productionMeasure(f.ctx, productionMeasureOptions{Duration: time.Duration(seconds) * time.Second, Cadence: roundCadence, ProbeOffset: roundCadence / 2, BandwidthCadence: time.Second, ProbeDeadline: roundDeadline, MaxGap: 5 * time.Second}, []*productionLoadTarget{target}, []net.Conn{probe}, f.carrierExits.failures, failures, t.Logf)
		cancelProfile()
		profileWG.Wait()
		rtts, overlap, timeouts, missed := productionSampleSummary(result.Samples, 0)
		up := float64(result.Upload[0]*8) / result.Elapsed.Seconds() / 1e6
		down := float64(result.Download[0]*8) / result.Elapsed.Seconds() / 1e6
		maxRTTDuration := productionPercentile(rtts, 1)
		maxRTT := maxRTTDuration.Milliseconds()
		t.Logf("supervised_result round=%d measured_ms=%d upload_mbps=%.3f download_mbps=%.3f rtt_samples=%d p50_ms=%d p95_ms=%d max_ms=%d timeouts=%d missed=%d overlap=%d failure=%v", round+1, result.Elapsed.Milliseconds(), up, down, len(rtts), productionPercentile(rtts, .5).Milliseconds(), productionPercentile(rtts, .95).Milliseconds(), maxRTT, timeouts, missed, overlap, measureErr)
		// Adjacent RTT variation is reported separately from the latency ceiling.
		// This is a laboratory metric, not the Speedtest app's private estimator.
		changes := productionRTTVariation(result.Samples, 0)
		if len(changes) > 0 {
			var total time.Duration
			for _, delta := range changes {
				total += delta
			}
			t.Logf("supervised_jitter metric=chronological_adjacent_rtt_absolute_difference samples=%d mean_ms=%.3f p95_ms=%.3f max_ms=%.3f target_ms=100 field_qualified=false", len(changes), float64(total)/float64(len(changes))/float64(time.Millisecond), float64(productionPercentile(changes, .95))/float64(time.Millisecond), float64(productionPercentile(changes, 1))/float64(time.Millisecond))
		}
		// Snapshot actual shared delivery state only after the timed interval.
		f.client.mu.Lock()
		clientGroup := f.client.stripe
		f.client.mu.Unlock()
		t.Logf("supervised_delivery side=client state=%+v", clientGroup.deliverySnapshot())
		t.Logf("supervised_scheduler side=client state=%+v", clientGroup.sessionOwner.pump.SchedulerSnapshot())
		f.mu.Lock()
		var serverGroup *stripeGroup
		for _, l := range f.serverLanes {
			serverGroup = l.stripe
			break
		}
		f.mu.Unlock()
		t.Logf("supervised_delivery side=server state=%+v", serverGroup.deliverySnapshot())
		t.Logf("supervised_scheduler side=server state=%+v", serverGroup.sessionOwner.pump.SchedulerSnapshot())
		for _, group := range []*stripeGroup{clientGroup, serverGroup} {
			group.mu.Lock()
			var live []*lane
			for _, carrier := range group.lanes {
				live = append(live, carrier)
			}
			group.mu.Unlock()
			for _, carrier := range live {
				carrier.serviceMu.Lock()
				ns, bytes, samples := carrier.serviceNS, carrier.serviceBytes, carrier.serviceSamples
				peerSamples, peerTarget := carrier.peerReadSamples, carrier.peerReadBatchTarget
				peerSlow, peerFast := carrier.peerReadSlowCount, carrier.peerReadFastCount
				peerLast := carrier.peerReadAt
				carrier.serviceMu.Unlock()
				peerAgeMS := int64(-1)
				if !peerLast.IsZero() {
					peerAgeMS = time.Since(peerLast).Milliseconds()
				}
				if carrier.side {
					local, remote, err := carrier.data.HeldByOrigin(carrier.account.ID, carrier.user.ID)
					t.Logf("supervised_physical account=%s server=%t peer_samples=%d peer_target=%d peer_slow=%d peer_fast=%d peer_age_ms=%d pending_bodies=%d active=%d held_local_bytes=%d held_local_messages=%d held_remote_bytes=%d held_remote_messages=%d hold_err=%v", carrier.account.ID, carrier.side, peerSamples, peerTarget, peerSlow, peerFast, peerAgeMS, carrier.peerReadPendingBodies.Load(), carrier.physicalActive.Load(), local.Bytes, local.Messages, remote.Bytes, remote.Messages, err)
				}
				t.Logf("supervised_service server=%t account=%s append_ms=%d encoded_bytes=%d samples=%d batch_target=%d active=%d unread=%d", carrier.side, carrier.account.ID, time.Duration(ns).Milliseconds(), bytes, samples, carrier.serviceBatchTarget(), carrier.physicalActive.Load(), carrier.unreceivedLocalBytes.Load())
				if carrier.diag != nil {
					d := carrier.diag.snapshot()
					t.Logf("supervised_batching server=%t account=%s bodies=%d records=%d useful_bytes=%d local_gc=%d local_gc_messages=%d headroom_waits=%d deferred=%d", carrier.side, carrier.account.ID, d.DataBatchCount, d.DataBatchRecords, d.DataPayloadBytes, d.CleanupLocalDataRuns, d.CleanupLocalDataMessages, d.LocalGrantHeadroomWaits, d.DeferredAdmissions)
				}
			}
		}
		assertLoadCarriersPreserved(t, f, carriers)
		if measureErr != nil {
			t.Fatal(measureErr)
		}
		qualifiedMbps := min(up, down)
		if seconds >= 60 {
			tailSeconds := min(60, seconds/2)
			tailUpload, tailDownload, tailDuration, completeTail := productionTailBandwidth(result.Bandwidth, seconds, tailSeconds)
			t.Logf("supervised_tail round=%d duration_ms=%d upload_mbps=%.3f download_mbps=%.3f", round+1, tailDuration.Milliseconds(), tailUpload, tailDownload)
			if !completeTail {
				t.Fatal("incomplete sustained tail windows")
			}
			qualifiedMbps = min(qualifiedMbps, tailUpload, tailDownload)
			if os.Getenv("ITOM_REWORK_QUALIFICATION") == "1" {
				u, d, duration, complete := productionTailBandwidth(result.Bandwidth, seconds, 10)
				if !complete {
					t.Fatal("incomplete final ten-second windows")
				}
				t.Logf("supervised_tail10 round=%d duration_ms=%d upload_mbps=%.3f download_mbps=%.3f", round+1, duration.Milliseconds(), u, d)
				qualifiedMbps = min(qualifiedMbps, u, d)
			}
		}
		tier, accepted := "mobile", false
		if mobile {
			gate := evaluateMobileLoad(t, result, baseline, working, n, mobileCap)
			accepted = gate.SpeedOK && gate.RTTOK
		} else {
			tier, accepted = qualificationPerformanceTier(t, qualifiedMbps, maxRTTDuration)
		}

		if goal == 100 && working == n && !rttPriority {
			// The temporary request replaces the former 800ms tier also in
			// non-mobile profiles; do not reject an allowed 850ms sample.
			tier, accepted = "temporary_100_mbps_10pct_lt1000ms", temporaryDuplexPerformanceGate(qualifiedMbps, maxRTTDuration)
		}
		if rttPriority && working == n {
			tier, accepted = "rtt_priority_hard75_mbps_lt1000ms", rttPriorityPerformanceGate(qualifiedMbps, maxRTTDuration)
		}
		if goal > 0 && working == n {
			goalOK := stableGoalSpeed(qualifiedMbps, goal)
			floor, tolerance := .9*goal, 10
			t.Logf("supervised_goal nominal_mbps=%.3f tolerance_percent=%d floor_mbps=%.3f minimum_verified_mbps=%.6f nominal_accepted=%t accepted=%t", goal, tolerance, floor, qualifiedMbps, qualifiedMbps >= goal, goalOK)
			if !rttPriority {
				accepted = accepted && goalOK
			}
		}
		if os.Getenv("ITOM_REWORK_QUALIFICATION") == "1" {
			minimumMbps := loadEnvFloat(t, "ITOM_LOAD_MIN_MBPS", 100)
			if !strictSpeedGate(minimumMbps, minimumMbps) {
				t.Fatal("invalid strict speed floor", minimumMbps)
			}
			tier = TransportRevision + "-strict-throughput-no-speed-tolerance"
			accepted = strictSpeedGate(qualifiedMbps, minimumMbps) && maxRTTDuration > 0 && maxRTTDuration < speedtestAcceptedMaxRTT && len(rtts) == roundExpected && timeouts == 0 && missed == 0 && overlap == roundExpected
			if os.Getenv("ITOM_STABILITY_QUALIFICATION") == "1" {
				tier = TransportRevision + "-stability-throughput-floor-waived"
				accepted = qualifiedMbps > 0 && maxRTTDuration > 0 && maxRTTDuration < speedtestAcceptedMaxRTT && len(rtts) == roundExpected && timeouts == 0 && missed == 0 && overlap == roundExpected
				if err := stabilityWindowQualification(result.Bandwidth, result.Elapsed, 1); err != nil {
					accepted = false
					t.Error(err)
				}
				t.Logf("supervised_stability windows=%d positive_duplex_every_window=%t throughput_floor_enforced=false", len(result.Bandwidth), accepted)
			}
			if err := speedtestRTTQualification(rtts, timeouts, missed, overlap); err != nil {
				accepted = false
				t.Error(err)
			}
			if os.Getenv("ITOM_STABILITY_QUALIFICATION") == "1" {
				minimumMbps = 0
			}
			if err := speedtestWindowQualification(result.Bandwidth, result.Elapsed, 1, minimumMbps); err != nil {
				accepted = false
				t.Error(err)
			}
		}
		t.Logf("supervised_acceptance round=%d tier=%s minimum_verified_mbps=%.6f max_rtt_ns=%d accepted=%t field_qualified=false", round+1, tier, qualifiedMbps, maxRTTDuration.Nanoseconds(), accepted)
		target.mu.Lock()
		targetTCP := len(target.connections)
		target.mu.Unlock()
		if targetTCP != len(sockets) {
			t.Fatal("target TCP identity changed", targetTCP, len(sockets))
		}
		t.Logf("supervised_identity round=%d application_sockets=%d target_tcp_dials=%d preserved=true", round+1, len(sockets), targetTCP)
		if target.firstError() != nil {
			t.Fatal("target integrity failed")
		}
		if !accepted || timeouts != 0 || missed != 0 || len(rtts) != roundExpected {
			// A complete performance FAIL must not suppress the next recovery
			// round. Integrity/partial echo/transport failures still stop above.
			t.Error("duplex performance/cadence gate", qualifiedMbps, maxRTTDuration, len(rtts), timeouts, missed)
		}
	}
	// Retire bulk producers without replacing the established echo socket or
	// logical session. An idle gap exercises quiet receive after heavy load.
	if os.Getenv("ITOM_REWORK_QUALIFICATION") == "1" {
		carriers := snapshotLoadCarriers(t, f, n, n)
		stop.Store(true)
		for _, p := range sockets[:len(sockets)-1] {
			p.Close()
		}
		workers.Wait()
		for i := 0; i < 6; i++ {
			if err := pause(f.ctx, 1100*time.Millisecond); err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			_ = probe.SetDeadline(started.Add(time.Second))
			payload := []byte(fmt.Sprintf("post-stress/%d", i))
			if err := productionWriteFull(probe, payload); err != nil {
				t.Fatal("post-stress write", err)
			}
			if _, err := readExactProbe(probe, make([]byte, len(payload)), payload); err != nil {
				t.Fatal("post-stress echo", err)
			}
			rtt := time.Since(started)
			if rtt >= time.Second {
				t.Fatal("post-stress RTT", rtt)
			}
			t.Logf("supervised_post_stress index=%d same_socket=true idle_gap_ms=1100 rtt_ms=%d", i+1, rtt.Milliseconds())
		}
		// A live old chat socket is insufficient if new application streams
		// cannot start after load. Verify a fresh bulk-sized round trip too,
		// then recheck the original socket without reconnecting the session.
		fresh, err := openApplication(target.echoConnect())
		if err != nil {
			t.Fatal("post-stress fresh application", err)
		}
		defer fresh.Close()
		_ = fresh.SetDeadline(time.Now().Add(5 * time.Second))
		payload := make([]byte, 256<<10)
		newProductionPattern("post-stress-fresh").fill(payload)
		written := make(chan error, 1)
		go func() { written <- productionWriteFull(fresh, payload) }()
		if _, err := readExactProbe(fresh, make([]byte, len(payload)), payload); err != nil {
			t.Fatal("post-stress fresh transfer", err)
		}
		if err := <-written; err != nil {
			t.Fatal("post-stress fresh write", err)
		}
		_ = probe.SetDeadline(time.Now().Add(time.Second))
		payload = []byte("original-after-fresh")
		if err := productionWriteFull(probe, payload); err != nil {
			t.Fatal("original socket after fresh transfer", err)
		}
		if _, err := readExactProbe(probe, make([]byte, len(payload)), payload); err != nil {
			t.Fatal("original echo after fresh transfer", err)
		}
		t.Log("supervised_post_stress_fresh bytes=262144 integrity_verified=true original_socket_rechecked=true")
		if f.client.logicalSession != logical || f.client.generation != generation {
			t.Fatal("post-stress session replaced")
		}
		// Snapshot actual shared delivery state only after the timed interval.
		f.client.mu.Lock()
		clientGroup := f.client.stripe
		f.client.mu.Unlock()
		t.Logf("supervised_delivery side=client state=%+v", clientGroup.deliverySnapshot())
		t.Logf("supervised_scheduler side=client state=%+v", clientGroup.sessionOwner.pump.SchedulerSnapshot())
		f.mu.Lock()
		var serverGroup *stripeGroup
		for _, l := range f.serverLanes {
			serverGroup = l.stripe
			break
		}
		f.mu.Unlock()
		t.Logf("supervised_delivery side=server state=%+v", serverGroup.deliverySnapshot())
		t.Logf("supervised_scheduler side=server state=%+v", serverGroup.sessionOwner.pump.SchedulerSnapshot())
		assertLoadCarriersPreserved(t, f, carriers)
	}

}

// Deliberate account outages happen before this snapshot. A reliable mobile
// stream may delay DATA, but must not silently replace a healthy carrier epoch.
// Retain pointers as well as epochs so a quick reconnect cannot hide a failure.
type loadCarrierSnapshot struct {
	id             string
	client, server *lane
}

func snapshotLoadCarriers(t *testing.T, f *productionSupervisorFixture, total, working int) []loadCarrierSnapshot {
	t.Helper()
	var snapshots []loadCarrierSnapshot
	for i := 1; i <= total; i++ {
		id := fmt.Sprintf("a%02d", i)
		if !f.network.accountAvailable(id) {
			continue
		}
		f.client.mu.Lock()
		client := f.client.lanes[id]
		f.client.mu.Unlock()
		f.mu.Lock()
		server := f.serverLanes[id]
		f.mu.Unlock()
		if client == nil || server == nil || client.ctx.Err() != nil || server.ctx.Err() != nil || !client.streamCarrierReady() || !server.streamCarrierReady() {
			t.Fatal("healthy account unavailable before measurement", id)
		}
		snapshots = append(snapshots, loadCarrierSnapshot{id, client, server})
	}
	if len(snapshots) != working {
		t.Fatal("working account count differs from declared case", len(snapshots), working)
	}
	return snapshots
}

func assertLoadCarriersPreserved(t *testing.T, f *productionSupervisorFixture, snapshots []loadCarrierSnapshot) {
	t.Helper()
	for _, old := range snapshots {
		f.client.mu.Lock()
		client := f.client.lanes[old.id]
		f.client.mu.Unlock()
		f.mu.Lock()
		server := f.serverLanes[old.id]
		f.mu.Unlock()
		if client != old.client || server != old.server || old.client.ctx.Err() != nil || old.server.ctx.Err() != nil {
			t.Fatal("spontaneous carrier outage/rejoin during load", old.id)
		}
	}
	t.Logf("supervised_carriers healthy=%d spontaneous_outages=0 epochs_preserved=true", len(snapshots))
}
func readExactProbe(c net.Conn, got, want []byte) (int, error) {
	n := 0
	for n < len(got) {
		k, e := c.Read(got[n:])
		n += k
		if e != nil {
			return n, e
		}
		if k == 0 {
			return n, fmt.Errorf("zero-byte read")
		}
	}
	for i := range want {
		if got[i] != want[i] {
			return n, fmt.Errorf("corrupt recovery echo")
		}
	}
	return n, nil
}

func supervisedSnapshots(ctx context.Context, t *testing.T, f *productionSupervisorFixture) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for index := 1; index <= 128; index++ {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		var lanes []*lane
		f.client.mu.Lock()
		for _, l := range f.client.lanes {
			lanes = append(lanes, l)
		}
		f.client.mu.Unlock()
		f.mu.Lock()
		for _, l := range f.serverLanes {
			lanes = append(lanes, l)
		}
		f.mu.Unlock()
		data := make([]laneProgress, len(lanes))
		var wg sync.WaitGroup
		callCtx, stop := context.WithTimeout(ctx, 200*time.Millisecond)
		for i, l := range lanes {
			wg.Add(1)
			go func(i int, l *lane) { defer wg.Done(); data[i] = l.progressSnapshot(callCtx) }(i, l)
		}
		wg.Wait()
		stop()
		sort.Slice(data, func(i, j int) bool {
			if data[i].Server != data[j].Server {
				return !data[i].Server
			}
			return data[i].Account < data[j].Account
		})
		raw, _ := json.Marshal(data)
		t.Logf("supervised_debt index=%d lanes=%s", index, raw)
		for _, l := range lanes {
			// Diagnostic only: distinguish a saturated physical DATA receiver
			// from queued account admission. Read each gate atomically under its
			// own mutex; never hold it during logging or transport I/O.
			if m := physicalMail(l.mail); m != nil && m.dialGate != nil {
				b := &m.dialGate.work
				b.mu.Lock()
				var queued [workClassCount]int
				for i := range queued {
					queued[i] = len(b.queues[i])
				}
				busy, service := b.busy, b.service
				b.mu.Unlock()
				t.Logf("supervised_account_work index=%d account=%s server=%t queued=%v busy=%v service_ns=%v", index, l.account.ID, l.side, queued, busy, service)
			}
			if l.diag == nil {
				continue
			}
			d := l.diag.snapshot()
			if l.stripe != nil {
				rawGovernor, _ := json.Marshal(l.stripe.deliverySnapshot())
				t.Logf("supervised_governor index=%d account=%s server=%t carrier_rtt_ms=%d governor=%s", index, l.account.ID, l.side, l.rtt.Load(), rawGovernor)
			}
			t.Logf("supervised_fast_wait index=%d account=%s server=%t queue_ms=%d queue_max_ms=%d grant_ms=%d grant_max_ms=%d stripe_ms=%d stripe_max_ms=%d data_ms=%d data_max_ms=%d", index, l.account.ID, l.side, d.FastPumpQueueWaitMS, d.MaxFastPumpQueueWaitMS, d.FastGrantSelectWaitMS, d.MaxFastGrantSelectWaitMS, d.FastStripeWaitMS, d.MaxFastStripeWaitMS, d.FastDataWaitMS, d.MaxFastDataWaitMS)
		}
	}
}

func logColdRamp(t *testing.T, c *applicationByteClock, seconds int) {
	t.Helper()
	var prevUp, prevDown int64
	for i := 1; i <= min(seconds, len(c.up)); i++ {
		up, down := c.totals(i)
		t.Logf("supervised_startup second=%d upload_mbps=%.3f download_mbps=%.3f cumulative_upload_mbps=%.3f cumulative_download_mbps=%.3f excludes_warmup=false", i, float64((up-prevUp)*8)/1e6, float64((down-prevDown)*8)/1e6, float64(up*8)/float64(i)/1e6, float64(down*8)/float64(i)/1e6)
		prevUp, prevDown = up, down
	}
}
