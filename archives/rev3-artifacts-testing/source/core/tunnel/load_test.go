package tunnel

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"sort"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
)

// Four independent IMAP accounts model a subset of the real ten-account pool.
// One application TCP stream remains owned by one Pump, while physical DATA is
// striped across every healthy carrier in the authenticated LogicalSession.
func TestLoadedApplicationRTT(t *testing.T)             { loadedApplication(t, false) }
func TestLoadedApplicationUploadWithTrace(t *testing.T) { loadedApplication(t, true) }
func loadedApplication(t *testing.T, uploadOnly bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 110*time.Second)
	defer cancel()
	cfg, _ := servercfg.New("load")
	u, err := cfg.AddUser("load")
	if err != nil {
		t.Fatal(err)
	}
	logical, generation, err := freshAttachment()
	if err != nil {
		t.Fatal(err)
	}
	clientStripe := newStripeGroup(logical, generation)
	serverHub := newStripeHub()

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		connection := 0
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			connection++
			discard := uploadOnly && connection == 2
			go func() {
				defer c.Close()
				if discard {
					n, _ := io.Copy(io.Discard, c)
					var b [8]byte
					binary.BigEndian.PutUint64(b[:], uint64(n))
					_, _ = c.Write(b[:])
				} else {
					_, _ = io.Copy(c, c)
				}
			}()
		}
	}()

	const carriers = 4
	runtimeFailures := make(chan error, carriers*2)
	workFailures := make(chan error, 8)
	serverDir := t.TempDir()
	clientDir := t.TempDir()
	var serverTrace, clientTrace *traceLog
	if uploadOnly {
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

	clients := make([]*lane, 0, carriers)
	clientDiags := make([]*diagnostic, 0, carriers)
	serverDiags := make([]*diagnostic, 0, carriers)
	for i := 0; i < carriers; i++ {
		a := servercfg.Account{ID: fmt.Sprintf("a%02d", i+1), Mail: mailbox.Config{Host: "mail.example", Port: 993, TLS: "tls", Username: "u", Password: "p"}}
		cm, sm := tlsMailPair(t, ctx, []string{u.Up, u.Down})
		sd := &diagnostic{tracer: serverTrace, account: a.ID, user: u.ID, side: "server"}
		cd := &diagnostic{tracer: clientTrace, account: a.ID, user: u.ID, side: "client"}
		serverDiags = append(serverDiags, sd)
		clientDiags = append(clientDiags, cd)
		sm = &observedMail{sm, sd}
		cm = &observedMail{cm, cd}

		go func(a servercfg.Account, sm mailbox.Transport, sd *diagnostic) {
			l, e := newLaneLogged(ctx, cfg.Server, u, a, filepath.Join(serverDir, a.ID), true, sm, sd)
			if e == nil {
				l.allowPrivate = true
				e = serverHub.attach(l)
			}
			if e == nil {
				e = l.run()
			} else if l != nil {
				l.close()
			}
			runtimeFailures <- e
		}(a, sm, sd)

		client, e := newLaneAttached(ctx, cfg.Server, u, a, filepath.Join(clientDir, a.ID), false, cm, cd, logical, generation, 1)
		if e != nil {
			t.Fatal(e)
		}
		if e = clientStripe.attach(client); e != nil {
			t.Fatal(e)
		}
		clients = append(clients, client)
		go func(l *lane) { runtimeFailures <- l.run() }(client)
	}

	for {
		ready := 0
		for _, l := range clients {
			if l.ready.Load() {
				ready++
			}
		}
		if ready == carriers {
			break
		}
		select {
		case e := <-runtimeFailures:
			t.Fatalf("runtime lane failed before readiness: %v", e)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}

	_, port, _ := net.SplitHostPort(listener.Addr().String())
	n, _ := strconv.Atoi(port)
	target := wire.Connect{Host: "127.0.0.1", Port: uint16(n)}
	owner := clients[0]
	probe, err := owner.open(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	checkRuntime := func(where string) {
		select {
		case e := <-runtimeFailures:
			t.Fatalf("runtime lane failed during %s: %v", where, e)
		default:
		}
	}
	probeIndex := 0
	probeRTT := func() time.Duration {
		checkRuntime("probe_start")
		probeIndex++
		start := time.Now()
		data := []byte("probe/0123456789")
		received := make([]byte, len(data))
		_ = probe.SetDeadline(time.Now().Add(25 * time.Second))
		if _, e := probe.Write(data); e != nil {
			t.Fatal(e)
		}
		if _, e := io.ReadFull(probe, received); e != nil {
			select {
			case failure := <-runtimeFailures:
				t.Fatalf("probe: %v; runtime: %v", e, failure)
			default:
				t.Fatal(e)
			}
		}
		if !bytes.Equal(data, received) {
			t.Fatal("corrupt probe")
		}
		d := time.Since(start)
		checkRuntime("probe_complete")
		t.Logf("probe_sample index=%d rtt_ms=%d", probeIndex, d.Milliseconds())
		return d
	}
	// Measure cold and steady idle RTT before bulk starts. One good warm sample
	// must not hide a slow first-byte path after OPEN/initial WINDOW exchange.
	idleSamples := make([]time.Duration, 0, 3)
	for i := 0; i < 3; i++ {
		idleSamples = append(idleSamples, probeRTT())
	}
	idleCold := idleSamples[0]
	sort.Slice(idleSamples, func(i, j int) bool { return idleSamples[i] < idleSamples[j] })
	idleP50 := idleSamples[len(idleSamples)/2]
	idleMax := idleSamples[len(idleSamples)-1]
	bulk, err := owner.open(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	defer bulk.Close()
	payload := bytes.Repeat([]byte("ITOMbulk01234567"), 2048)
	var total atomic.Int64
	dataDone := make(chan time.Duration, 1)
	start := time.Now()
	go func() {
		var e error
		for total.Load() < 32<<20 {
			var n int
			n, e = bulk.Write(payload)
			total.Add(int64(n))
			if e != nil {
				break
			}
		}
		if e == nil {
			e = bulk.CloseWrite()
		}
		if e != nil {
			workFailures <- e
		}
	}()
	go func() {
		b, e := io.ReadAll(bulk)
		if e == nil {
			if uploadOnly {
				if len(b) != 8 || binary.BigEndian.Uint64(b) != uint64(total.Load()) {
					e = io.ErrUnexpectedEOF
				}
			} else if int64(len(b)) != total.Load() || !bytes.Equal(b, bytes.Repeat(payload, len(b)/len(payload))) {
				e = io.ErrUnexpectedEOF
			}
		}
		if e != nil {
			workFailures <- e
			return
		}
		dataDone <- time.Since(start)
	}()
	time.Sleep(500 * time.Millisecond)
	var samples []time.Duration
	for i := 0; i < 8; i++ {
		samples = append(samples, probeRTT())
	}
	// Keep a fixed verified workload: stopping after probes made short RTT
	// trials transfer fewer bytes and over-weight cold-start/FIN drain time.
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	loadedP50 := samples[len(samples)/2]
	loadedMax := samples[len(samples)-1]
	t.Logf("striped_carriers=%d loaded_probe idle_cold_ms=%d idle_p50_ms=%d idle_max_ms=%d p50_ms=%d max_ms=%d bulk_bytes_so_far=%d upload_only=%t", carriers, idleCold.Milliseconds(), idleP50.Milliseconds(), idleMax.Milliseconds(), loadedP50.Milliseconds(), loadedMax.Milliseconds(), total.Load(), uploadOnly)
	// This bounded build smoke test checks directional useful throughput, never
	// the doubled echo aggregate. It has four carriers and artificial command
	// delay, without an independently measured mobile baseline. Full production
	// qualification belongs to the supervised/mobile workload, not these tiers.
	var elapsed time.Duration
	select {
	case elapsed = <-dataDone:
	case e := <-runtimeFailures:
		t.Fatalf("runtime lane failed under load: %v", e)
	case e := <-workFailures:
		t.Fatalf("bulk transfer failed: %v", e)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	checkRuntime("post_load")
	directionalMbps := float64(total.Load()*8) / elapsed.Seconds() / 1e6
	aggregateMbps := directionalMbps
	if !uploadOnly {
		aggregateMbps *= 2
	}
	t.Logf("striped_carriers=%d command_delay_ms=80 bytes_per_direction=%d idle_cold_ms=%d idle_p50_ms=%d idle_max_ms=%d loaded_p50_ms=%d loaded_max_ms=%d bulk_ms=%d directional_payload_mbps=%.3f aggregate_payload_mbps=%.3f", carriers, total.Load(), idleCold.Milliseconds(), idleP50.Milliseconds(), idleMax.Milliseconds(), loadedP50.Milliseconds(), loadedMax.Milliseconds(), elapsed.Milliseconds(), directionalMbps, aggregateMbps)
	for _, set := range []struct {
		name  string
		diags []*diagnostic
	}{{"client", clientDiags}, {"server", serverDiags}} {
		var appendCount, appendBytes, controlAppendCount, controlAppendBytes, dataAppendCount, dataAppendBytes, fetchPolls, emptyFetchPolls, fetchMessages, idleWaits, idleNotifies, idleAudits, idleKicks, dataAttempts, dataRecords, dataPayloadBytes, dataBatchCount, dataBatchRecords, dataBatchBodyBytes, bulkReplacements int64
		var maxControlWait, maxDataWait, maxFastDataWait, maxLocalGrantWait, maxRemoteGrantWait, localGrantHeadroomWaits, localGrantLimitWaits, maxPumpQueueWait, maxFastPumpQueueWait, maxStripeWait, maxFastStripeWait, maxGrantSelectWait, maxFastGrantSelectWait, maxCleanup int64
		var peakDataHeldBytes, peakDataHeldMessages uint64
		connections := 0
		for _, d := range set.diags {
			s := d.snapshot()
			appendCount += s.AppendCount
			appendBytes += s.AppendBytes
			controlAppendCount += s.ControlAppendCount
			controlAppendBytes += s.ControlAppendBytes
			dataAppendCount += s.DataAppendCount
			dataAppendBytes += s.DataAppendBytes
			fetchPolls += s.FetchPolls
			emptyFetchPolls += s.EmptyFetchPolls
			fetchMessages += s.FetchMessages
			idleWaits += s.IdleWaits
			idleNotifies += s.IdleNotifies
			idleAudits += s.IdleAudits
			idleKicks += s.IdleKicks
			dataAttempts += s.DataAttempts
			dataRecords += s.DataRecords
			dataPayloadBytes += s.DataPayloadBytes
			dataBatchCount += s.DataBatchCount
			dataBatchRecords += s.DataBatchRecords
			dataBatchBodyBytes += s.DataBatchBodyBytes
			maxControlWait = max(maxControlWait, s.MaxControlWaitMS)
			maxDataWait = max(maxDataWait, s.MaxDataWaitMS)
			maxFastDataWait = max(maxFastDataWait, s.MaxFastDataWaitMS)
			maxLocalGrantWait = max(maxLocalGrantWait, s.MaxLocalGrantWaitMS)
			maxRemoteGrantWait = max(maxRemoteGrantWait, s.MaxRemoteGrantWaitMS)
			localGrantHeadroomWaits += s.LocalGrantHeadroomWaits
			localGrantLimitWaits += s.LocalGrantLimitWaits
			peakDataHeldBytes = max(peakDataHeldBytes, s.PeakDataHeldBytes)
			peakDataHeldMessages = max(peakDataHeldMessages, s.PeakDataHeldMessages)
			maxPumpQueueWait = max(maxPumpQueueWait, s.MaxPumpQueueWaitMS)
			maxFastPumpQueueWait = max(maxFastPumpQueueWait, s.MaxFastPumpQueueWaitMS)
			maxStripeWait = max(maxStripeWait, s.MaxStripeWaitMS)
			maxFastStripeWait = max(maxFastStripeWait, s.MaxFastStripeWaitMS)
			maxGrantSelectWait = max(maxGrantSelectWait, s.MaxGrantSelectWaitMS)
			maxFastGrantSelectWait = max(maxFastGrantSelectWait, s.MaxFastGrantSelectWaitMS)
			bulkReplacements += s.BulkReplacementGrants
			maxCleanup = max(maxCleanup, s.CleanupMS)
			connections += s.IOConnections
		}
		avgRecords := 0.0
		if dataAttempts > 0 {
			avgRecords = float64(dataRecords) / float64(dataAttempts)
		}
		avgAppendBytes := 0.0
		if appendCount > 0 {
			avgAppendBytes = float64(appendBytes) / float64(appendCount)
		}
		avgDataMIMEBytes := 0.0
		if dataAppendCount > 0 {
			avgDataMIMEBytes = float64(dataAppendBytes) / float64(dataAppendCount)
		}
		avgBatchRecords := 0.0
		if dataBatchCount > 0 {
			avgBatchRecords = float64(dataBatchRecords) / float64(dataBatchCount)
		}
		mimeToPayload := 0.0
		if dataPayloadBytes > 0 {
			mimeToPayload = float64(dataAppendBytes) / float64(dataPayloadBytes)
		}
		t.Logf("perf_diag side=%s io_connections=%d append_count=%d append_bytes=%d avg_append_bytes=%.0f control_append_count=%d control_append_bytes=%d data_append_count=%d data_append_bytes=%d avg_data_mime_bytes=%.0f data_payload_bytes=%d mime_to_payload_ratio=%.3f data_batch_count=%d data_batch_records=%d avg_batch_records=%.2f data_batch_body_bytes=%d fetch_polls=%d empty_fetch_polls=%d fetch_messages=%d idle_waits=%d idle_notifies=%d idle_audits=%d idle_kicks=%d data_attempts=%d data_records=%d avg_records_per_data_attempt=%.2f max_control_wait_ms=%d max_data_wait_ms=%d max_fast_data_wait_ms=%d max_local_grant_wait_ms=%d max_remote_grant_wait_ms=%d local_grant_headroom_waits=%d local_grant_limit_waits=%d peak_data_held_bytes=%d peak_data_held_messages=%d max_pump_queue_wait_ms=%d max_fast_pump_queue_wait_ms=%d max_stripe_wait_ms=%d max_fast_stripe_wait_ms=%d max_grant_select_wait_ms=%d max_fast_grant_select_wait_ms=%d bulk_replacements=%d max_cleanup_ms=%d", set.name, connections, appendCount, appendBytes, avgAppendBytes, controlAppendCount, controlAppendBytes, dataAppendCount, dataAppendBytes, avgDataMIMEBytes, dataPayloadBytes, mimeToPayload, dataBatchCount, dataBatchRecords, avgBatchRecords, dataBatchBodyBytes, fetchPolls, emptyFetchPolls, fetchMessages, idleWaits, idleNotifies, idleAudits, idleKicks, dataAttempts, dataRecords, avgRecords, maxControlWait, maxDataWait, maxFastDataWait, maxLocalGrantWait, maxRemoteGrantWait, localGrantHeadroomWaits, localGrantLimitWaits, peakDataHeldBytes, peakDataHeldMessages, maxPumpQueueWait, maxFastPumpQueueWait, maxStripeWait, maxFastStripeWait, maxGrantSelectWait, maxFastGrantSelectWait, bulkReplacements, maxCleanup)
	}
	checkRuntime("latency_assertion")
	select {
	case e := <-workFailures:
		t.Fatalf("bulk transfer failed before latency assertion: %v", e)
	default:
	}
	tier, accepted := qualificationPerformanceTier(t, directionalMbps, loadedMax)
	label := "historical_tier_diagnostic_field_qualified_false"
	if raceInstrumented {
		label = "instrumented_measurement_not_qualification"
	}
	t.Logf("%s tier=%s minimum_verified_mbps=%.6f max_rtt_ns=%d accepted=%t upload_only=%t", label, tier, directionalMbps, loadedMax.Nanoseconds(), accepted, uploadOnly)
	// This fixture verifies payload integrity, bounded work and owner survival.
	// CPU-dependent localhost Mbps are diagnostic; qualification uses the
	// independently shaped access profile and fixed application-byte window.
	t.Logf("functional_load_complete directional_mbps=%.6f max_application_rtt_ms=%d field_qualified=false", directionalMbps, loadedMax.Milliseconds())
	cancel()
	for i := 0; i < carriers*2; i++ {
		select {
		case <-runtimeFailures:
		case <-time.After(5 * time.Second):
			t.Fatal("striped worker did not stop")
		}
	}
}
