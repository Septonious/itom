# Useful-delivery controller: isolated 1.1.0 experiment

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
