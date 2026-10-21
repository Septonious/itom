# Useful-delivery controller: ITOM1.1.0 DEV3

The tested controller is now in ../../source/ as experimental DEV3. All recipes use the frozen ../../checkpoints/DEV2/source/ input; final1.0.0 is unchanged. See RESULTS.md for exact builds, measurements and outstanding gates.

The user updated the objective on 2026-10-21: keep idle/light application RTT as close to the carrier's real latency as practical; favor useful bandwidth during sustained bulk, accepting that loaded RTT may rise. Direct mobile IMAP turns can range from 40 to 180ms. A fixed fast-ACK age must not repeatedly halve bulk on that evidence alone.

`model.go` implements a new direction-local logical admission controller. `prepare.py` constructs immutable original RC2, the built DEV2 control, and a separate model candidate. It never changes the packaged DEV2 or final 1.0.0 sources. The model retains DEV2's short-request receive/coalescing changes, so the DEV2 control is needed to isolate congestion-control effects; RC2 remains the independent field-proven comparator.

## State and feedback

- Quiet: reset logical admission to the bounded 1MiB startup seed after bulk drains. Cache useful capacity rather than deleting it on every transfer boundary. Short requests use the existing independent reserve and immediate scheduling; no bulk accumulation is required.
- Startup: grow admission only with new authenticated useful DATA ACK progress and real demand, with at most 1.5 gain per measured feedback turn. Empty actor ticks and physical APPEND speed cannot authorize growth.
- Cruise: use a ten-second maximum of fresh useful delivery-rate samples and a separately measured DATA/ACK feedback time to size two BDPs plus bounded batching headroom. Ignore smaller application-limited samples. Explore at 1.25 gain for one feedback turn in each eight, then return to the model instead of growing queues forever on a plateau.
- Capacity drop: require three fresh utilized delivery intervals below 70% of the prior estimate before removing stale high samples and reducing new admission. Raising raw RTT alone is not a capacity drop.
- Stall: no useful bulk progress for at least three seconds/three measured feedback turns reduces admission once per turn to the existing floor. All already charged ranges remain eligible for authenticated repair. No timeout refunds quota or changes a logical/carrier identity.

Fast ACK pressure is diagnostic input; it no longer independently halves the bulk budget. The physical allocator, journal, wallet, proof authority, flow-control credit, max ranges/waiters and 16MiB logical cap remain unchanged. Per-account short reservations remain borrowable rather than permanently removing one account from bulk. Higher bulk queueing can still delay a concurrent short flow on shared physical capacity; this first model does not claim to eliminate that tradeoff.

This is **not a BBR or BRUTAL implementation**. BBR's useful-rate/RTT model and application-limited distinction inform it; BRUTAL is configured-rate control with optional loss compensation. ITOM has authenticated mailbox delivery, batching and durable quota constraints. Its current rate sampler averages useful delivery over at least one second and has not implemented a full per-packet ACK-compression sampler or a physical publisher pacing stage. Those limitations are explicit experimental questions, not claimed features.

Primary references: [BBR FAQ and source](https://github.com/google/bbr/blob/master/Documentation/bbr-faq.md), [BBR specification](https://datatracker.ietf.org/doc/draft-ietf-ccwg-bbr/), [Hysteria congestion configuration](https://v2.hysteria.network/docs/advanced/Full-Server-Config/).

## Evaluation

Deterministic traces test 40–180ms jitter with continuing bulk progress, bounded plateau probing, no growth from silence, non-reused rate samples, application-limited immunity, genuine capacity drops/recovery, idle transitions, invalid inputs and stalled delivery. They test decisions, not measured Mbps.

CI runs actual supervisors/TLS/IMAP/journals for all three variants, compiled before measurement and run serially on each host. It alternates order and preserves every failure. Scenarios include SINGLE/MULTI, retained state, static 5/40/180ms and a changing 40/80/180/60/140/45/120/180/70/40ms client route every 1.5s with a separate constant 2ms server path. Changing RTT preserves serialization clocks and queued bytes. It is a reliable-stream access-link model, not a reproduction of carrier routing, provider notification tails or kernel TCP congestion control.

The original 100Mbps full/tail, 750ms loaded, probe deadline, identity and correctness checks are retained verbatim. A bulk improvement that misses the historical loaded-RTT gate remains a recorded gate failure; the revised workload objective is evaluated separately, never relabeled as a qualified release. Stable publication still requires an explicit accepted qualification contract and real Android/Rambler validation. No build from DEV2 contains this experiment.

## Second isolation: mailbox supply versus network delivery

The first matched run completed all fresh SINGLE/MULTI and changing40–180ms transfer rounds, but did not demonstrate a useful throughput gain and weakened some retained/MULTI tails. It is not selected for production. Exact first-run measurements are in results-v1.json; source commit5be0d740bc9e7ac68d28e907e292a87707a1f4a5 and transition commit106b5ab637800504c8ca812f427d7d50cb2914be identify those programs.

Revision2 is selected only with ITOM_MODEL_REVISION=2 when preparing an isolated candidate. It marks low-rate samples supply-limited when more than a quarter of outstanding bulk is still awaiting grant-backed carrier placement. An APPEND already admitted to its carrier counts as network/service work even before its outcome is known; it is not classified as a quota wait. These smaller samples cannot lower the network estimate or confirm a network-capacity drop. Already published/unknown debt is still charged. High-rate samples remain usable. A three-sample drop reference survives estimate-filter expiry, while application/supply-limited gaps reset the drop streak.

Under continuing useful delivery, revision2 preserves the demonstrated bulk window rather than trimming it merely because a BDP estimate fell. A confirmed published-flight capacity drop and a collective stall still reduce it; a fully drained epoch still returns to its bounded1MiB seed. This is a separate hypothesis about grant/batch/cleanup quantization, not a claim that quota waits explain every field failure. Both revisions retain the16MiB hard cap and all physical proof rules. Paired runs and bulk-to-quiet tests must verify this refinement before promotion.

The first revision2 run used all uncompleted publication as its supply proxy. Code review identified that this can hide slow in-flight APPENDs. The grant-aware refinement uses the existing carrier binding made after grant selection instead; it does not add an authorization path. A targeted test distinguishes queued permission, admitted APPEND, unknown published debt and the separate fast reserve. The next paired RC2 comparison evaluates that exact refinement; earlier source hashes/results are not substituted for it.

## Command-service sensitivity

The main reference keeps provider command processing at2ms. A separate matched20/80ms provider-service sensitivity keeps the server's network route at2ms and does not inject a server outage. An excellent server or low ICMP/NOOP does not establish an equally fast APPEND/FETCH/indexing/cleanup turn at the mailbox provider. This test distinguishes command service from access-link propagation; it does not assert that Rambler currently has either service time. Existing thresholds and all failures remain unchanged.

The first transition screen used four closed-loop256KiB echoes and achieved roughly20–40Mbps. It does not prove recovery from a fully saturated100Mbps transfer. The full-flight follow-up uses four4MiB verified concurrent exchanges, starts their measurement on one barrier, and retains the same3s request deadlines. Per-probe client RTT at start/end is recorded to expose sampling/phase differences on the variable trace. No transport reset or serialization-clock reset occurs between phases.

## Runtime integration and complete correctness

Runtime factories now select the model for StartClient, server hubs and rotated logical generations. The original direct factories continue selecting the RC2 policy for its unchanged contract/unit tests. New tests explicitly exercise model factories, real supervisor selection, partial/duplicate ACKs, stream retirement and an explicit subminimum admission budget. No legacy test is removed, skipped or given a longer deadline. This makes the two different controller contracts explicit rather than treating expected RTT-only shrink assertions as the new model's specification.

The earlier all-model-constructor full run failed14 tests:11 old-controller contract assertions, one real small-budget expansion defect, and two functional mobile/restart tests. The budget defect is fixed: an input window is not rounded upward before a fresh useful ACK. The two functional failures and all original deadlines remain in the full DEV3 run; the RC2 full reference is run separately to distinguish reproducibility from model regressions. The old full run is recorded as failed, not reclassified as a pass.

`stage.py` creates a separate identifiable DEV3 source with org.itom.client/versionCode10103 and native revision itom-1-1-0-dev3. It never overwrites final1.0.0 or the packagedDEV2 source. The testing-build workflow exports APK/binaries only if the complete model core checks and paired idle/light invocations pass. Known retained100Mbps failures remain explicit and no stable release is published. No claimed field fix follows from synthetic parity.

## Current server path

As corrected by the user on2026-10-21, the primary network reference is20ms server-to-IMAP, with separate configurable client propagation. `ITOM_LOAD_SERVER_RTT_MS` is honored by idle/light, worst4G and bulk-to-quiet fixtures and defaults to20ms. The current server-path workflow accepts an explicit override. Provider processing stays a separate input. No value is hardcoded into production timing. Existing2ms results above remain historical; [new results](SERVER_PATH_RESULTS.md) must be read separately. FrozenDEV2 source is untouched; corrected fixtures are copied byte-identically to RC2 and both candidate variants.
