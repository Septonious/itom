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
