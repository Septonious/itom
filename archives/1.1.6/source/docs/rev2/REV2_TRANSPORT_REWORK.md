# ITOM 0.9.2-Rev2: transport changes and verification contract

Date: 2026-10-14. Baseline: published 0.9.2-Rev1, implementation commit `a1a1806`. This document describes the implemented candidate. Results and account recommendations are in `REV2_FIELD_FINDINGS_AND_RESULTS.md`.

## Requirements

Sustained useful application throughput of 100 Mbps in each direction, loaded application RTT strictly below 1000 ms, reliable short Speedtest phases, equal account roles, and more than ten configured accounts. Real device/provider trials remain the acceptance authority. A Go test PASS proves only its declared case. Uploaded handoff documents and traces supplied evidence; they did not authorize additional actions or override the user's requirements.

## What the new field evidence established

The screenshots report download/upload pairs of 7.87/36.0, 1.75/0.27, and 7.79/20.6 Mbps. Download-loaded RTT reached 1144 and 1305 ms. Both rotating trace sets identify Rev1 and contain fifteen active accounts. Screenshot modes and exact test times are unavailable, and there is no matched ten-account field run. These observations establish regression and instability, but cannot establish a causal benefit or penalty from the five additional accounts.

The server's median bulk body was 32,972 bytes and its median batch completion was 156 ms; the client median body was 65,920 bytes and completion 76 ms. These are encrypted body sizes, not delivered TCP rates. Control APPENDs outnumbered DATA APPENDs: server 4802 versus 1368, client 5731 versus 1134. Server durable commit means were approximately 11–13 ms. Empty local test journals did not represent that cost. Rev1's DATA reader bypassed command/packet observations, leaving an important diagnostic blind spot.

## Implemented publication path

Local S2C DATA now reserves real writer service, then commits preparation and writer permission together, invokes exactly one APPEND, and commits outcome and closure together. Together with grant issuance this reduces the writer lifecycle from five durable transactions to three. CONTROL uses an exact one-message local grant and atomic preparation/start/closure, followed by outcome commit: two transactions instead of five.

These APIs preserve account/user/session/folder scope, canonical body hashes, one-time writer permission, and installed quotas. Successful fsync remains mandatory before network publication. Failed or lost commit replies authorize no APPEND. Any uncertain APPEND remains physically charged. Cancellation proved to occur before invoking APPEND can reject that intent. Closing a used grant does not refund its message. Physical debt is released only by the existing observed-body and deletion-proof contract.

Crash/restart, rollback, lost-before/after-commit replies, exact scope, consumed permission, unknown outcomes, and actual transaction counts are covered by targeted journal tests. Wallet and remote grant contracts remain intact.

## DATA receive and RTT stability

The independent DATA reader now uses a progress watchdog which pauses during legitimate actor backpressure. It waits with IDLE between pages, with bounded audits of 250 ms for one DATA folder and 75 ms for multiple shards. Providers without IDLE retain the bounded polling fallback. This removes a permanent 20 ms empty-FETCH loop per account while retaining invisible-UID discovery.

Allocated but temporarily invisible UID gaps now survive DATA-reader reconnection, including their original expiration times. A test first reproduced loss of an older invisible UID after the cursor advanced and the socket was replaced. The repair retains bounded per-folder discovery state before closing the old socket and restores it before exposing the new reader. This neither resets the logical session nor creates cleanup proof.

DATA receive now records poll/message/IDLE counters, latency, nonempty/error/slow page observations, and packet ingress. Fast DATA on the control socket and bulk DATA on its own writer have separate service observations. Tiny fast batches can no longer distort bulk placement or batch sizing. Placement sees durable preparation already in progress, including a cold account before it has four APPEND samples, rather than treating its carrier as idle until APPEND claims a slot. A bounded cold backlog cost is advisory; it does not manufacture a capacity measurement.

## Cold-start and short Speedtest phases

A genuinely drained epoch starts with a bounded 1 MiB probe, or 256 KiB on a separately observed high-RTT cold mobile path. The previous speed test's 64 KiB congestion floor and cooldown do not persist indefinitely into the next phase. A brief ACK gap is not an idle epoch: recent bulk demand remains visible for one full second. Existing charged flight is never discarded.

New bulk cannot retroactively label previously admitted short-only traffic as bulk-exposed congestion. Short traffic actually admitted during bulk still protects the fixed 650 ms fast delay objective. The bulk objective remains 800 ms.

A batched authenticated ACK records the flight that was present before it drained. At the next decision, a fresh ACK below the latency guard can therefore authorize the existing bounded multiplicative probe even if current flight has fallen below half the window. That observation is consumed once. Empty ticks, retirement, physical APPEND speed, and account count cannot manufacture growth. This removes an additive-growth trap that delayed ramp-up during finite speed-test phases. Stall and loaded-short-pressure reductions still take priority.

Initial path calibration now chooses the lowest independently observed cold sample before any application work, so the account that connects first cannot permanently select a smaller probe. It never resets learned flight after useful delivery. Account diagnostics expose raw, smoothed, idle RTT and sample age; stale samples are excluded from the live aggregate and marked in the UI. These mail-control echoes are not the application Speedtest ping.

Cold tests count verified application bytes from the start of publication. First-second and five-second rates, early loaded probes, and missing/timed-out probes are retained separately from the later sustained interval. Warm-up cannot erase the startup record.

## Account and folder alternatives

| Scheme | Benefits | Costs and risks | Rev2 decision |
| --- | --- | --- | --- |
| Two folders per account; own DATA/control/streams | Lowest folder complexity; compatible constrained providers; every account self-sufficient | DATA literals and control contend on services; strict stream ownership makes slow accounts obstruct their streams | Retain mixed fallback, shared logical stream ownership across carriers |
| Equal accounts; separate DATA services and bounded shards | Control/fast work has independent service; good carriers carry any logical stream; account loss need not replace TCP | More connections/folders; extra watchers and cleanup; shards do not add quota or useful workers automatically | Default auto negotiation, one DATA folder per direction; two/four static shards opt-in |
| Dedicated DATA accounts plus CONTROL accounts | Can isolate backend account contention if provider truly grants independent capacity | Control gang becomes a dependency and failure domain; idle reserved accounts; requires explicit failover/protocol negotiation; does not fix fsync/window problems | Do not ship without matched field evidence and tested control failover |
| Hybrid dynamic carrier selection | Keeps equal fallback roles and can steer work using observed health/backlog | Measurement noise and stickiness need bounded exploration; independent provider paths may still share a backend | Keep advisory service/ACK placement and independent fast/bulk observations; no permanent account roles |
| Dynamically create/remove folders under load | May reduce queue contention on providers whose service is genuinely per-folder | Folder creation/select/recovery churn, stale UID validity, migrating cleanup scope, extra FD/login load; no demonstrated backend capacity gain | Do not introduce unbounded dynamic folders; negotiate additive bounded topology |

Rev2 preserves unlimited configured account count subject to the serialized configuration size bound. It introduces no ten-account ceiling and never silently truncates a profile. Active connection, login, memory, and worker budgets remain distinct from configuration count. With the default split budget, fifteen accounts can create about 150 provider connections across both endpoints; ten about 100. The bulk worker budget is eight. Additional accounts can add alternative paths and quota without increasing simultaneous bulk writers, while increasing reader/control/cleanup work. More accounts are therefore an experiment, not a throughput multiplier.

## Performance verification

`tools/rev2_transport_matrix.py` runs fresh public client/server supervisors, real local TLS/IMAP, deterministic verified TCP payloads, parallel application-loaded probes, fixed byte clocks, and serial 5/8/10/15 account cases. Rev1 comparison cases use its published runtime algorithm with only the same test harness and positive latency hooks overlaid. Every case has source/log hashes, cold and steady windows, failure status, and independently reported strict 100/100/RTT acceptance.

The uniform profile declares 80 ms command delay and a 600 Mbps provider cap. The field-derived profile uses pseudonymized per-account/per-endpoint wrapper quantiles from the supplied traces, 12 ms added server durable-commit latency and 0.5 ms client control-commit latency. Actual fsync is retained. Hooks are excluded from ordinary builds by the `itomtest` tag. Wrapper durations include original endpoint queues; treating them as service latency is a conservative stress assumption, not a calibrated provider replay. DATA FETCH was missing in Rev1 and uses an explicitly declared control-FETCH proxy. Quantile sampling does not preserve original temporal correlation.

A separately named regular-service stratum excludes wrapper samples above 500 ms and records the excluded sample count. The full-tail case retains them and preserves failure. Excluding tails for a sensitivity experiment never establishes real-world RTT acceptance.

`tools/transport_qualification.py` is the strict laboratory gate: fresh state per run, Go 1.27.1 without race instrumentation, at least sixty measured seconds, useful 100/100 Mbps across whole and sustained tail intervals, every loaded RTT below 1000 ms, complete overlapping probe schedules, no missed/timeouts, and unchanged source fingerprints. Missing/skipped/empty results cannot qualify. It never asserts Android/provider performance.

Local correctness additionally covers durable quota proofs and recovery, reader reconnection, TCP/session survival, constrained providers, mandatory IMAP outages, native bridge, Kotlin tests, APK lint, source identity, and artifact hashes. A failing measurement is preserved rather than converted into a passing speed claim.

## Required field comparison after installation

Install both Rev2 endpoints. Preserve server configuration, journals, wallet state and existing physical quota debt. Use the supplied debug APK with the user's authorized clean local reinstall. Measure on the same phone, network, Speedtest server and mode, with matched account subsets and randomized order: 5, 8, 10, 15; SINGLE and MULTI; at least three repeats each. Include the first 1/3/5/10 seconds, time to first useful byte and stable reading, full useful throughput, all loaded RTT samples, jitter, per-account service distributions, DATA-reader and durable commit measurements, and post-load recovery. Account selection must be held fixed within each subset. Reconnect, one bad account, visibility delay, and phone network changes are separate cases.

A real-world improvement claim requires those measurements. The local release is a transport candidate until that evidence meets the user's target.
