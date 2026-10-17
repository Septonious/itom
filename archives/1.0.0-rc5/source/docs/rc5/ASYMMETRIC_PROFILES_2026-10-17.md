# RC5 asymmetric DATA journal tests — 2026-10-17

All results here are **laboratory only**, not live IMAP/provider, and not a release qualification. Original published RC2 settings and deployed 12 MiB accounts remain unchanged. The RC5 branch's test-only `ITOM_EXPERIMENT_SERVER_PROFILE=16` seeds **fresh** 16 MiB journals on the Linux/server side using an already allowlisted profile; the Android/client side is deliberately kept at the existing 12 MiB profile. It refuses combination with retained-state qualification. No production default, migration, physical accounting or crypto rule is altered.

## Ten accounts, 24-stream duplex

- Go 1.27.1, 4 execution threads, 80 ms modeled command delay, modeled 600 Mbps provider, 60-second load with 10-second warmup and real TLS/IMAP fixture.
- **Server 16 MiB / client 12 MiB:** download **79.399 Mbps**, upload **141.754 Mbps**; last 30 seconds 80.216 / 141.890 Mbps, last 10 seconds 81.527 / 148.269 Mbps. Loaded RTT p50 283 ms, p95 381 ms, max **395.830 ms**. All 30/30 probes overlapped useful duplex work, 0 timeouts and 0 misses.
- 49/49 original application target TCP connections remained and six same-socket post-load exchanges plus fresh 256 KiB transfer passed.
- Cold ramp still started slowly: first-second download 11.010 Mbps; fifth second 151.519 Mbps; tenth second 102.236 Mbps. Transient peaks are not qualification.
- **Strict throughput case failed**, solely because sustained download <100 Mbps. There is no license to publish, tag or change a deployed DATA profile based on this test.

This distinguishes the observed server-outbound headroom limitation from the previously failed experiments enlarging *both* endpoint journals. Next isolate server 24 MiB/client 12 MiB entirely in disposable local test source without adding a non-allowlisted journal profile to GitHub or production. Even if a single laboratory case reaches 100/100 and <750ms, independent fresh/retained SINGLE/MULTI repeats and real Android/provider confirmation remain necessary.

No deployment, release, migration or material performance guarantee is implied.


## Server 24 MiB / client 12 MiB (local disposable, not a supported production profile)

A separate test-only local source changed the **server's** physical DATA journal to 24 MiB / 384 messages and extended ONLY the local compatibility fixture. This value is NOT present in the committed production profile allowlist. Ten accounts, 24 concurrent application streams per direction, 80ms modeled IMAP commands, modeled 600 Mbps provider, four Go execution threads, fresh state, 60-second full measurement:

- **Download 106.959 Mbps; upload 44.661 Mbps** over the full 60 seconds.
- Last 30 seconds: download **111.455 Mbps**, upload **32.130 Mbps**.
- Last 10 seconds: download **111.175 Mbps**, upload **16.803 Mbps**.
- Loaded RTT median 309ms, p95 402ms, maximum **489.774ms**.
- 30/30 overlapping probes, zero misses and zero timeouts. Original carrier/application survival and post-load exchanges were checked; **strict 100/100 throughput FAIL**.
- Client delivery controller entered `CONGESTED` with observed ACK RTT ~2.39s, oldest bulk range ~2.78s and fast acknowledgment age ~2.47s while the server-side sender remained `NORMAL`. Several server accounts held ~11–12 MB of locally published outbound DATA at the end of the measurement.

**Result:** Enlarging *only* server DATA journal did NOT prevent upstream starvation. Additional outbound capacity on the server alone cannot satisfy simultaneous upload/download targets. The 24 MiB profile is REJECTED; no code, journal migration, or operational recommendation uses it. The experiment strongly motivates measuring bidirectional peer feedback and DATA-read service latency rather than further buffer expansion.

GitHub retains only the safe, allowlisted **server 16 MiB / client 12 MiB** test runner and documented failed results. Next software experiment should preserve RC2's 12 MiB production default, the two fast-carrier reserves, and the proof-based physical quota lifecycle.


## Rejected ACK routing/coalescing experiments (isolated test copies)

Both cases used server 24 MiB/client 12 MiB, 10 accounts, 24 streams per direction, 60-second load and the same strict no-tolerance acceptance checks. **Neither change was committed to the production runtime.**

- **Promote all established bulk ACK/WINDOW updates into the existing urgent control queue:** download **144.724 Mbps**, upload only **2.464 Mbps** in the first ten-second measured interval; test aborted on a 6.0-second upload progress blackhole. **REJECT**. The original queued class/service fairness must be preserved.
- **Double ordinary cumulative bulk-feedback coalescing interval from 20 to 40 ms:** full download **111.568 Mbps**, full upload **24.493 Mbps**; last 30 seconds download **111.385**, upload **8.223 Mbps**; last 10 seconds download **107.269**, upload **11.403 Mbps**. Maximum loaded RTT **405.248 ms**, all 30/30 RTT probes successful. **REJECT**. Saving some control APPEND activity cannot substitute for prompt upload feedback.

Next isolated experiment preserves the original control queue and 20 ms coalescing, and removes only the delivery controller's **additional rate-dependent clamp after its guarded halving** to test whether its own throttled rate artificially reinforces an ACK slowdown. All actual pressure/outage decisions, original 50% reduction, physical journal spending, no-replay invariants and fast reserves remain intact. No qualification claimed without full results.


## Rejected congestion-RTT simplification

A one-change experiment removed only the extra **rate-derived clamp** following the logical delivery governor's mandatory guarded 50% window reduction; outage/short-pressure decisions and default feedback routing remained intact.

Server 24 MiB/client 12 MiB, 10 accounts, 24 streams/direction, 60 seconds: download **110.070 Mbps**, upload **27.700 Mbps**, last 30 seconds **110.590/14.759**, last 10 seconds **109.943/13.107** Mbps. Max loaded RTT **392.471ms**, all 30/30 probes completed. **Strict 100/100 FAIL.** The rate-clamp change is rejected and was reverted in the local experiment. The remaining deficit is authenticated upload feedback/service scheduling, not a single bad reduction formula.

Next isolated hypothesis tests backlog-aware bulk feedback routing **only among existing eight non-reserved eligible carriers**, with the original control class, 20ms coalescing and physical quota unchanged. Do not commit that route change unless it meets duplex performance and unchanged stability tests.


## 12 MiB versus server-only 24 MiB: control and receive counters (October 17)

The new **development-only** `rc5_duplex_feedback` per-client-account counters were compared on two independent 10-account / 24-stream / 60s fresh-state modeled-provider runs. These aggregates include warmup and maintenance; they are not per-second TCP goodput.

| Counter/measurement | 12 MiB server / 12 MiB client | 24 MiB server / 12 MiB client |
| --- | ---: | ---: |
| Full application download Mbps | 74.012 | 108.095 |
| Full application upload Mbps | 152.878 | 34.891 |
| Max loaded echo RTT ms | 498 | 412 |
| Client-side physical CONTROL APPENDs, 10 accounts total | 6722 | 6744 |
| Physical CONTROL bytes, client-side, 10 accounts total | 13,680,944 | 14,068,140 |
| Client-side DATA FETCH MIME bytes, 10 accounts total | 912,809,965 | 1,291,951,896 |
| Per-account client DATA callback maximum wait | 0 ms in recorded precision | 0 ms in recorded precision |
| Client-side local cleanup runs, 10 accounts total | 1048 | 622 |

The nearly unchanged **physical CONTROL APPEND count** does not support the simple theory that a new flood of tiny controls causes the 24 MiB failure. Increased physical client DATA FETCH bytes confirm greater downstream load. Only client-side diagnostic counters were available in this run, so these cannot establish the server-to-client ACK sealing or visibility time; these must be measured explicitly. Upload collapsed in the server-only 24 MiB run even though all ten client-side remote grant waits reported zero and DATA callback handoff delays were negligible. No counters contain account credentials, payloads, mailbox IDs or cryptographic material.

An isolated test-only shared server DATA publication pacer is now being evaluated to see whether physical batch **burstiness**, rather than average control volume, causes bidirectional starvation. Its speed and RTT must be measured before any consideration of committing transport code. Neither extra 24 MiB quota nor pacer is part of the published/deployable source.


## Rejected server-wide DATA publication pacing A/B

An isolated **server-only** MIME DATA-byte rate pacer was inserted before any local grant selection, fsync, or physical APPEND, preserving short/control classes and the two quiet carriers. A shared per-session pacing timestamp spread out large batches while preserving physical quota and retry proof. The pacer itself has been removed from the experimental runtime after the test; it has **never been merged** into the RC5 branch.

Server 24MiB/client 12MiB, 10 accounts, 24 streams per direction, 80ms modeled IMAP command delay, modeled 600Mbps provider, 60s sustained, GOMAXPROCS=4. Nominal 180 Mbps **physical MIME payload** send ceiling:
- Full window download **107.903 Mbps**, upload **31.173 Mbps**; last30s **111.009/10.661 Mbps**, last10s **111.935/9.227 Mbps**.
- Loaded RTT max **385.574ms**, 30/30 probes, no misses/timeouts. Original and fresh connections recovered after load.
- Client delivery governor observed up to 4.07s bulk ACK age (oldest range 4.07s) and 29 short-pressure/congestion decisions; server remained NORMAL.
- **Strict FAIL**. Smoothing downstream bursts at this ceiling did not restore upload; the bottleneck needs more than static physical outbound rate pacing. Do not promote this to deployment.
