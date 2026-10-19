# RC10.1 real-network field analysis — 2026-10-19

The user confirmed VLESS was OFF during ITOM tests. Cyan/purple screenshots are MULTI; orange/green is SINGLE. The earlier VLESS/443 Moscow–Sweden measurement is a separate path reference, not direct IMAP latency or a hard access-link capacity measurement.

## Screenshots

| Mode | Download Mbps | Upload Mbps | Idle RTT ms | Download-loaded RTT ms | Upload-loaded RTT ms | Jitter ms |
|---|---:|---:|---:|---:|---:|---:|
| MULTI | 20.8 | 28.7 | 379 | 427 | 649 | 85 |
| MULTI | 4.97 | 4.14 | 376 | 567 | 741 | 139 |
| MULTI | 9.23 | 3.88 | 363 | 587 | 526 | 86 |
| SINGLE | 14.1 | 23.5 | 369 | 457 | 512 | 121 |

All four screenshots show completed measurements. The supplied data does not establish whether any additional attempts failed. MULTI varies markedly; three observations are insufficient to conclude it is always worse than SINGLE. The similar 363–379ms idle RTT across all four results points to a persistent transport-path floor rather than a problem only at peak load. These measurements do not meet the earlier sustained 100Mbps objective.

## Evidence coverage and method

- Android export: `1.0.0-rc10.1-debug`; server capture: `itom-1-0-0-rc10-1`, Go 1.27.1. Both contain the new stage profiler. Ten account IDs are represented on each endpoint.
- Client retained trace: approximately 16:55:28–16:57:46 Moscow time. Server: 16:56:17–16:58:24. Approximate wall-clock overlap is 89 seconds; no one-way durations were calculated by subtracting endpoint clocks.
- Each endpoint retained one trace run. Rotation removed earlier events from an approximately twenty-minute session. The Android diagnostics were exported with phase OFF and an empty current transport snapshot. Retained health events are therefore the live-state evidence, not the final OFF snapshot.
- Stage histogram figures below use last-minus-first cumulative histograms independently for each account observer. No counter reset was detected. The client and server snapshot intervals differ; these aggregates are not paired-request latency decompositions. Histogram p95 values are bucket upper estimates. Exact tail examples come from single-endpoint batch timelines.
- Retained health snapshots report zero trace drops. File rotation still limits coverage. Sampled frame counts and sampled batch timelines are not complete event populations.

## Findings

### 1. The server durable-journal path contributes substantial latency and explicit spikes

Mean stage durations from the snapshot deltas:

| Stage | Client interactive | Server interactive | Client bulk | Server bulk |
|---|---:|---:|---:|---:|
| Account/grant selection | 0.096ms | 64.642ms | 0.036ms | 33.057ms |
| Journal preparation | n/a (wallet path) | 34.126ms | n/a | 32.820ms |
| Wallet spend | 0.468ms | n/a | 0.830ms | n/a |
| DATA APPEND | 51.621ms | 77.014ms | 101.534ms | 81.451ms |
| Final accounting | 0.284ms | 29.213ms | 0.011ms | 29.110ms |
| Physical batch total, excluding earlier selection | 52.476ms | 140.652ms | 99.065ms | 169.971ms |

Selection includes grant reservation and any durable work in that operation. Individual stage means must not be added to claim application RTT: the populations and timing boundaries differ.

Three independent server-account batch timelines end together around 16:56:32 Moscow time:

- Interactive batch: **594.368ms total**, including **500.529ms journal preparation**, **52.104ms APPEND**, **41.555ms final accounting**.
- Interactive batch: **622.601ms total**, including **71.705ms preparation**, **59.017ms APPEND**, **491.680ms final accounting**.
- Bulk batch: **693.210ms total**, including **124.879ms physical-slot wait**, **473.761ms preparation**, **67.994ms APPEND**, **25.914ms final accounting**.

These directly locate much of those particular spikes in the server's durable path, rather than pure IMAP network time. Across data/control stores, snapshot deltas show approximately **19.59ms per journal transaction** and **3.35ms mutex queue time** on the server, versus **0.657ms** and **0.0019ms** on the phone. The transaction timer includes the entire bbolt Update; these logs do not isolate fsync, filesystem, VM storage, transaction processing or scheduler effects. They do not prove faulty server hardware. Journal `Failures` also includes expected business-rule rejections, so its nonzero value is not proof of disk errors.

### 2. Useful delivery/ACK completion is much slower than local account selection

| Logical range completion | Client mean / estimated p95 upper | Server mean / estimated p95 upper |
|---|---:|---:|
| Interactive admission-to-ACK | 390.542 / 768ms | 541.514 / 1500ms |
| Bulk admission-to-ACK | 743.317 / 1500ms | 657.454 / 1500ms |
| Interactive accepted-publication-to-ACK | 336.703 / 768ms | 320.674 / 1000ms |

ACK timing includes forward delivery and acknowledgement return; it is neither one-way mailbox visibility nor Speedtest RTT. Its long duration reduces useful progress and makes a throughput controller alternate between flight growth and pressure reductions.

The client-side selector is fast (mean 0.096ms interactive, 0.036ms bulk), as are encryption/packing (bulk about 0.4ms; short traffic much less) and FETCH delivery callbacks (microsecond-scale). Allocating another reserved account is not the first indicated remedy for the measured journal/ACK delays.

### 3. Window growth and download batch selection warrant careful tuning after reducing the floor

Server snapshots during active transfer show shared logical window growth from approximately **1MiB to 9.9MiB** and a separate pressure reduction from about **1.74MiB to 0.87MiB**. Client snapshots also show growth, recovery and congestion reductions. These are shared session controller snapshots, not ten independent windows. Growth/reduction is normal control behavior; the logs establish oscillation, not that each decision is wrong.

Among retained server bulk batches, **236/461 (51.2%)** had physical body size at most 40KiB; the median body size was **32,972 bytes**, while some reached **362,452 bytes**. Server batch-target snapshots include 32, 64, 256 and 336KiB. Cold/aged downstream observations can legitimately return the target to 32KiB. The supplied data does not establish which reductions were justified; receiver-receipt training and cold resets need a focused audit.

Client bulk admission had **4848 deferrals / 5311 attempts**; server bulk had **2533 / 3003**. These are repeated nonblocking admission attempts, not failed TCP requests or lost packets. They show backpressure but do not alone prove the window is too small. Blindly shrinking flight or permanently capping batches would risk repeating earlier throughput regressions.

### 4. Control/cleanup and historical recovery consume significant work

Retained client trace: **6603 control APPENDs versus 1549 DATA APPENDs**. Server: **4545 control APPEND completions versus 1429 DATA APPENDs**. Count ratio is operation overhead, not bandwidth ratio. The client also recorded **542 WALLET_HISTORY_MISSING deferrals**, spread across all ten accounts.

The recovery code emits that message when an old server grant has no matching client wallet history. Reinstallation or client-state reset while retaining server journals is one possible cause, but these archives do not establish when or why the histories diverged. The implementation correctly refuses to invent a zero-spend proof. This recurring historical reconciliation adds work; no hard quota exhaustion was demonstrated by these stage samples. Server held-message snapshots reach 173/192 but include reservations and retained debt, not only unread payload.

### 5. Retained evidence does not show carrier collapse during the measured client window

No new client reconnect lifecycle events were retained, and reconnect counters do not increase between retained health snapshots. One account already had one earlier reconnect; its cause is outside retained coverage. Retained client DATA/CONTROL APPEND operations have no IMAP error completions.

The server's 86 retained target-dial events report ready/ok or management/ready, with no target-dial failure. For non-management ready/ok events, DNS/connect timings must be interpreted from per-operation detail, not the absent top-level duration field.

Client socket CLOSED/RESET events cluster around groups of Speedtest streams ending; these are application sockets, not proof of IMAP lane failures. Server-wide timeout/recovery events occur around **16:58:11–12**, approximately 25 seconds after the client trace ends and the export shows OFF. That pattern is consistent with peer disappearance at the end of the test session; it does not establish a load-induced carrier outage.

## Recommended next work, in order

1. **Reduce durable transaction amplification and tails on the server.** Measure transaction/commit and storage latency under the actual workload; audit repeated grant/prepare/finalize/cleanup writes. Coalesce eligible metadata changes or use correctly ordered group commit where useful. Keep durable authorization before APPEND, all unknown-outcome debt and proof requirements; do not disable fsync or discard journal state.
2. **Shorten and stabilize delivery/ACK service.** Audit control batching/ACK scheduling, receive wake/visibility waits and historical-recovery traffic. Back off repeated unresolvable offers without cancelling recovery obligations or inventing proofs. Preserve short-flow fairness.
3. **Then tune flight growth and downstream batch learning against measured useful progress.** Bound overshoot relative to the actual baseline and retain enough flight for throughput. Check whether repeated cold-path resets are unnecessarily serializing 32KiB publications. Keep numerical qualification thresholds unchanged and verify each candidate against RC10 on the same network.

No source modification, new binary or qualification claim is part of this analysis. Raw client/server archives remain private in the analysis workspace; this report contains aggregate timings and redacted stage examples only.
