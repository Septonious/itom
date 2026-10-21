# ITOM 1.1.0 development

Isolated baseline: the frozen final 1.0.0 / original RC2 transport. Development source: `source/`; Android remains `org.itom.client`, version code10102. This is DEV1, **not a qualified release**. No 1.0.0 source or acceptance threshold is changed.

First experiments replace the interaction of exponential hot polling with a demand-scoped notification search, and introduce per-socket quiet/burst accumulation timing. Bulk batch, stream credit, physical quota, journal, wallet, recovery and liveness policies remain the reference policies. The observed transport forwards optional scheduling capabilities so diagnostics do not silently change the controller.

Local first-stage demand-only screening: three AB/BA/AB pairs, ten accounts, independent 2ms server route and 5/40ms client routes, actual supervisors/TLS/IMAP/journals. All12 invocations completed their original sockets. At5ms pooled idle mean60.2→50.9ms and light133.0→64.1ms; at40ms idle124.9→171.3ms worsened while light133.5→121.3ms improved. The40ms tradeoff blocks acceptance of this prototype; it is retained for investigation, not called a universal improvement. Full logs will accompany the experiment report.

Calibration found that the shared load fixture replaced its configured phone bandwidth with unlimited when the legacy aggregate cap was absent. The new shared fixture preserves the explicit path in that case; explicit client speed changes still affect only the client route. Both reference and candidate receive the exact same fixture correction. The existing local host has two CPU cores of quota and runs both endpoints plus the provider; earlier comparisons used four-core CI. CPU profiles show substantial carrier/cleanup parsing and hashing, but do not establish CPU as the only cause of timeouts.

The workflow performs full correctness/race checks and matched fresh/retained SINGLE/4/MULTI, binary capabilities and20ms server-route sensitivity. It keeps the original loaded probes/tails/sockets and requires100Mbps in each direction. Results include reference failures; no partial rate is called sustained throughput. Stable publication and field-qualified claims are blocked pending evidence and real Android/Rambler confirmation.

## First matched CI result and next isolation

[Run39595927793](https://github.com/Septonious/itom/actions/runs/39595927793), commit57bf27f, includes72 idle and72 light verified requests per version at each5/40/100ms client route. Candidate idle means35.6/87.2/180.1ms versus RC252.6/95.5/196.7; light33.7/85.0/178.1 versus51.2/100.3/189.7. All latency invocations passed. Fresh4/MULTI, BINARY and historical20ms route passed for both; retained SINGLE/MULTI remained roughly76Mbps upload for both, failing the unchanged100Mbps target. Candidate SINGLE completed two rounds but reached787ms once against750ms; RC2 SINGLE timed out before a full measurement. No complete rate comparison is claimed for that partial control. All exact rounds/probes and raw-log hashes are in experiments/ci1-results.json.

The full tunnel correctness suite passed750.962s. The common overlay accidentally reintroduced RC2's obsolete4s mailbox-test parent against an unchanged8s production watchdog. It is now copied from the already corrected frozen1.0.0 final fixture; the production watchdog and qualification criteria are untouched. The corrected mailbox suite passes locally, along with journal/wallet/flow/datapath race tests. The following run repeats the complete suite for the changed source.

The next isolated change pipelines a replacement fast lease only after its durable cumulative report exhausts all original message permissions. It does not close the old lease or refund its debt; unknown outcomes and all original quota profiles are preserved. Its targeted test checks partially used grants, duplicate reports, unproved debt, historical reservations, normal bulk grants, physical deletion proofs and final audit. This correction is under evaluation, not a claimed retained-throughput fix.

A separate worst4G replay uses the exact supplied ping sequence, asymmetric1Mbps down/2.5Mbps up and2ms server path. It completed78 verified16KiB request/replies over30.276s,0.338Mbps each direction with no connection replacement. That0.1Mbps connectivity objective does not replace the high-capacity100Mbps gate.

Development build jobs may export clearly marked unqualified DEV1 APK/binaries only after correctness and latency checks succeed. Failed performance gates remain failed and no stable release is published. Build notes include the exact commit and qualification result.

## Current selection: low-path notification search only

The second independent matched run, [39598387014](https://github.com/Septonious/itom/actions/runs/39598387014), retained the5ms mean benefit (idle57.2→34.3ms, light57.5→36.2ms) but worsened40ms idle99.7→103.2ms and light120.2→137.0ms, with worse tails. SINGLE candidate passed both complete100Mbps/750ms rounds this time, while RC2 failed its RTT gate; that does not isolate the effect of fast rollover. Retained upload remained~76Mbps and failed100Mbps for both. Exact results remain in experiments/ci2-results.json.

The current candidate therefore retains the new notification search only when a measured unloaded provider command turn is positive and at most20ms. That bounds its extra IDLE/DONE turn relative to the old hot polling interval. Unknown or slower routes keep the RC2 receive policy. Quiet/burst socket coalescing remains separate. The normal journal/wallet/grant code is restored exactly, and the rollover implementation/tests are preserved in experiments/deferred-fast-rollover.patch for an isolated later comparison. No unproved debt is released and no quota configuration is increased.

This is an iteration of the1.1 transport work, not a declaration that the entire rework or release qualification is complete. The current source must pass its own comparisons; earlier checkpoint results are not substituted for it.

## DEV2 recovery guard

The complete DEV1 run803d1fa failed TestRev7CappedMixedSocketsRepairWithoutLosingOriginalStream after its original12s transfer deadline, despite passing every fresh high-speed case. Packaging was automatically skipped. DEV2 restricts adaptive response waits to providers with independent DATA/maintenance capacity as well as a measured<=20ms route and working IDLE. Capped shared services retain RC2 timing. Five consecutive targeted capped-repair and controller checks passed16.807s locally without changing the deadline. Full correctness and matched qualification are repeated for DEV2 before its test artifacts are exported. The fast-rollover patch remains deferred; grant/journal/wallet policies are unchanged.

The long-idle harness now accepts a test-only pause parameter; ordinary runs keep their previous exact150ms/800ms cadence. A first3000ms comparison was stopped and marked contaminated because a compile overlapped it. It is not used as evidence. The rerun compiles both binaries before measurement and runs them serially without competing work.
