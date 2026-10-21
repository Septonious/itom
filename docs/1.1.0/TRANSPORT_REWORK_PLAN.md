# ITOM 1.1.0 transport rework

Decision: final 1.0.0 restores RC2; all new transport work is isolated. Primary objective is the lowest practical idle/light-load application RTT, including tails and time to first byte, while preserving RC2 throughput and stability. The new deployment has a user-reported 2ms server-to-imap.rambler.ru path. This is an input to measurement, not a replacement for measuring provider visibility, command turns, durable IO or target latency. More than100Mbps on ten accounts and intermittent sub200ms account echoes are field observations, not an already demonstrated simultaneous idle/application/throughput guarantee.

## 1. Establish the physical floor before redesign

Measure phone and server direct TLS NOOP, APPEND, first visible UID, IDLE notification-to-BODY and complete FETCH times separately. Use monotonic local spans and authenticated causal request IDs; do not subtract unsynchronized wall clocks. Distinguish account control echo, persistent TCP application echo, fresh OPEN/TLS, direct network ICMP, first byte and loaded ACK residence. Measure Android scheduling, fsync queue/commit and GC pauses, without logging payloads or credentials. A 2ms ICMP path does not prove a2ms mailbox turn. Provider visibility and sequential command turns may dominate. Report median/p95/p99/max with all failures; do not optimize the displayed RTT metric.

## 2. Carrier-aware scheduling and explicit state machine

Replace interactions among global ticks, polling hints, cleanup and physical grants with explicit events and bounded per-account/per-direction work queues. Keep IMAP session/protocol ownership in the carrier adapter; isolate logical streams and durable accounting. States: quiet, response-pending, light/bursty, sustained bulk, draining, reconnecting. Transitions use actual queue/byte demand and authenticated progress, with hysteresis and bounded expiry. An open but inactive socket alone must not keep ten accounts polling. Do not permanently dedicate an extra account if idle capacity can be safely borrowed; RC2's existing quiet-reader reservation is the comparator.

Derive short wait windows from measured command/notification distributions and recent successful response turns. Experiment independently with tiny-write coalescing, provider-RTT-scaled hot polling, notification-driven reentry, wake coalescing, burst-aware deadlines and actor timer separation. Bound empty FETCH/IDLE churn, CPU, wakeups and control MIME. Requests get immediate wakeups on their likely return carriers; replies do not cause a reciprocal polling storm. Quiet paths remain event-driven. Never use loaded tunnel RTT as a pure propagation baseline or shorten reconnect/liveness deadlines to chase a latency goal.

## 3. Separate latency scheduling from storage authority

Use predicted completion time per account: queued work + measured APPEND/visibility/BODY service + confirmed availability. Keep minimum/idle propagation, service distribution and queue residence separate. Favor fresh short requests without starving larger TLS records, control, recovery or bulk. Preserve per-stream ordering while allowing independent streams and carriers to progress. Classify by observed size/rate/age rather than destination/Speedtest special cases. Limit exploration and keep a stable incumbent when path estimates overlap; account heterogeneity tests must show no oscillation.

## 4. Dynamic batching, pacing and window budgets

Keep three different limits explicit: authenticated receiver credit; logical congestion/admission budget; physically durable bytes/message permissions. None creates authority for the others. Size useful in-flight logical bytes from measured useful delivery and baseline feedback time, with a queue-delay budget and hard memory/quota caps. Use pacing and gradual growth with direction-specific evidence; do not halve/double on every actor tick or on one noisy ACK. Preserve bulk batch efficiency under sustained load. Tiny messages can bypass accumulation only when the extra record/MIME/fsync cost is measured and bounded. Re-evaluate receive WINDOW advertisement and update coalescing against actual consumer backlog; never advertise unreserved memory or let ACK delay stall a light request.

A low RTT should permit fewer logical bytes in flight for the same rate, but blanket small fixed windows already failed. At100Mbps,20ms useful feedback needs roughly250KiB;200ms needs2.5MiB, before safety margin. These are illustrative BDP values, not physical grants or a new hard ceiling. Retransmission/repair decisions use authenticated stream progress and measured variance; preserve unknown APPEND charges, epochs and replay protection. Do not stack KCP/QUIC onto IMAP solely because DNS/WebRTC projects use them.

## 5. Durable state and upgrade design

Keep quota journal/wallet invariants as separate independently tested components. No refund without the existing physical/proof rules. Retain unknown outcomes and missing-wallet debt. Re-evaluate deferred reclaimed-descriptor and no-op commit fixes independently with crash/replay/corrupt-proof tests. Any schema/wire change needs explicit version negotiation, migration validation and rollback documentation; do not pretend a new server repairs lost client state. Provision a persistent signing key privately before regular APK updates; org.itom.client stays fixed, version codes increase, and profile IDs/wallet paths survive updates. Existing temporary keys cannot be retroactively matched.

## 6. Experiments and acceptance

Every candidate starts from the same RC2 production manifest. Copy the same test-only healthy endpoint fixtures to both; compile before measurement and run serially on the same host. Alternate AB/BA order across independent repetitions, retain all trials, report host CPU/quota and fsync variability. The first sweep is hypothesis screening; select nothing from one best run. Reject any statistically clear throughput regression and repeat inconclusive comparisons. No percentage allowance replaces an existing absolute gate.

Matrix: ten accounts/twenty directions; independent server2ms/1Gbps and phone5/20/40/50/100ms/500Mbps; server20ms sensitivity for attribution; zero artificial server outage in the primary reference. Use verified32B/512B/4KiB request/response and fresh OPEN/TLS after quiet intervals; light unidirectional and bursty loads; SINGLE/4/32 streams; separate download/upload phase and tails; simultaneous bulk plus short traffic; fresh and retained state, uneven accounts and long UID history. Repeated Speedtest-like connection startup/cancellation must retain carrier/session/socket identity. Provider latency/notification/visibility tails are a distinct sensitivity suite, not automatically blamed on a faulty server.

Preserve the exact existing qualification scripts/thresholds: the historical75Mbps full60s/30s-tail/10s-tail duplex floor and750ms loaded maximum where those original gates apply; strict100Mbps targets remain separate and unchanged. Passing75 is never called proof of sustained100. Keep complete probes, positive per-second useful duplex, zero failures and original sockets. Additional objective: repeated idle/light distribution improvement versus matched RC2, aiming below200ms and minimizing it further without speed loss. Do not use sub200ms as a fabricated guaranteed network floor.

Keep accounting/recovery/race tests even with an excellent server: phone interruptions, process crashes, cancellation, missing notifications and lost wallets remain possible. Label intentional server blackouts as optional legacy stress, separate from healthy deployment acceptance. Replay the user's worst raw4G case independently:1Mbps down/2.5Mbps up, ping sequence84,63,43,67,23,60,55,56,21,66ms; require at least0.1Mbps useful messaging connectivity and bounded completion, not an impossible100Mbps floor on that path.

## Calibration finding from the first sweep

Five isolated prototypes have been measured and preserved in [experiments/RESULTS.md](experiments/RESULTS.md). No change is accepted yet. The combined-host local reference also failed on unmodified RC2 and did not reproduce known field throughput. Diagnose that fidelity gap before using its numerical rates to select the new transport. No server-fault assumption or threshold waiver follows from a synthetic failure.

## Delivery milestones

1. Instrument and calibrate the new2ms server plus original RC2; freeze a reproducible field baseline.
2. Screen isolated timing/coalescing experiments, then repeat promising pairs with full throughput/tail and overhead controls. Initial local results and exact patches are in experiments/; none changes final1.0.0.
3. Build the event-driven carrier/queue scheduler behind an explicit experimental flag; compare each component to RC2, retaining only measured non-regressions.
4. Introduce independent dynamic pacing/window/batching controllers, with deterministic unit traces and adversarial recovery tests. Avoid coupling many knobs in one candidate.
5. Run full matched fresh/retained matrix and crash/recovery/signing migration checks. Only then build a1.1 prerelease for the new server.
6. Validate repeated real Android/Rambler SINGLE/MULTI, idle and light probes with diagnostics enabled/disabled. Local synthetic results are a selection tool; field failures block promotion.

## Workload objective revision, 2026-10-21

The user now explicitly prioritizes different operating points: minimize baseline/light-load waiting, and maximize useful bulk bandwidth when demand is sustained, accepting higher loaded RTT. Mobile IMAP latency can vary from40 to180ms even on a good connection. Do not infer congestion from that RTT movement alone or repeatedly shrink bulk because a concurrent short ACK crosses a fixed budget. Existing historical qualification outcomes remain recorded with unchanged thresholds; a changed workload objective is not permission to relabel failed checks.

The first implementation is the isolated [useful-delivery model](../../archives/1.1.0/experiments/delivery-model/README.md): authenticated rate sampling, application-limited protection, direction-local BDP flight, bounded startup/probing, sustained-capacity-drop confirmation, and an independent collective-stall guard. Pair RC2, DEV2 and model variants, with a changing40–180ms path and bulk-to-quiet transitions on persistent probe sockets. Production1.0.0 and packagedDEV2 remain frozen while this model is evaluated. Future physical pacing must account for mailbox batching and quota service; copying QUIC packet pacing or BRUTAL loss compensation without those measurements is not part of this implementation.
