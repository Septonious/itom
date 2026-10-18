# ITOM RC2–RC7 change ledger (2026-10-18)

**Baseline:** RC2, last relatively stable live-provider build. **Current state:** RC7 16/12 MiB rescue is a *test candidate*, not released. Every entry distinguishes an observed laboratory result from a fully qualified release.

## Non-negotiable RC7 contract

- Android package ID **`org.itom.client`**, no `applicationIdSuffix`; version `1.0.0-rc7` / versionCode 10006. RC2–RC6 debug APK ID `org.itom.client.dev.rev7` is different; Android private profiles will not migrate automatically.
- At full simulated provider capacity: **upload ≥75.000 Mbps AND download ≥75.000 Mbps** for the 60-second duplex measurement and trailing 30s/10s windows; **maximum loaded application RTT ≤750ms**. No acceptance based on a one-way result, sum, transient peak, loose rounding or former throughput-floor waiver.
- Stable original TCP sockets and recovery, 30/30 scheduled 2s cadence RTT probes with none missed/timed out, positive duplex activity in all 60 one-second windows, sustained 4/32 stream loads, low-link acceleration and separate high→low downgrade, provider blackout, fresh/retained journal, quota/rekey/reconnect, Go correctness/race, Android test+lint+JNI, Linux builds and subsequent real Rambler/provider field verification. Ramp phases artificially restricted below 75Mbps cannot themselves meet the full-capacity throughput requirement; enforce connection survival and RTT separately.
- Never change the durable journal, quota debts, physical APPEND/DELETE proof rules or identities to optimize synthetic throughput. Never publish a release if the gates are red.

## Verified source diff from RC2 to RC6

The archived RC2→RC6 core diff has 19 changed paths, predominantly diagnostics/tests. RC5 introduced (1) faster ACK-conditioned small-window growth in `tunnel/delivery_policy.go` and (2) faster BODY batch cold/warm ramp in `tunnel/service_policy.go`. These additions assist throughput but are implicated in burst pressure; reverting either alone was not effective. RC6 changed `deliveryMaxWindow` from **16MiB → 8MiB**; retained journal and quota rules unchanged. Latest STATUS timestamp, bulk-ACK residence and physical FETCH timing changes were observational.

## Experiment ledger

| Experiment | Upload Mbps | Download Mbps | Max loaded RTT | Outcome |
|---|---:|---:|---:|---|
| **RC5 asymmetric: 16 MiB server / 12 MiB client** | **141.8** | **79.4** | **395ms** | Best reported **single** 60s, 10-account lab run, 30/30 RTT probes; numerically passes 75/75 and ≤750ms. Crucial caveat: the exact asymmetrical test patch was not found as a bit-identical published commit. Reconstruct and re-test, never assert immutable equivalence. |
| RC6 original 8MiB old 12-scenario matrix | ≥77.665 | ≥67.471 | 486ms | Old 4-stream lab qualification passed; **fails current 75 download requirement**, and real phone speedtests reset at startup/acceleration. |
| RC6 original 8MiB, new 32 streams | 47.6 | 27.0 | Probe timeout | Fails ~21 seconds into 60s test. |
| FieldFix1 fast-carrier placement, 8MiB | ~50 | ~27 | ~1,100ms | 65 original sockets survived / 30 RTT replies; **FAIL** speed and latency (correct fail-closed CI independently ~1,011ms). |
| FieldFix2 placement + 4MiB, 32 streams | 41.06 | 32.07 | 601ms | Steady 150Mbps/60s RTT/sockets PASS, throughput **FAIL**. Separate 10Mbps startup timed out and real 20→50→150 ramp reached ~1,450ms **FAIL**. |
| FieldFix2 independent steady CI | 40.55 | 31.95 | 621ms | Same stable sockets, still throughput FAIL. |
| Adaptive authenticated-rate admission | 19.3 | 17.9 | 432ms | Steady RTT good, throughput FAIL, ramp reached 808ms; rejected. |
| RC7 RC2-growth + RC2-batch (4 streams) | 56.12 | 25.34 | 738ms | Throughput FAIL. |
| RC7 RC2-growth + RC2-batch (32) | 43.85 | 30.61 | 801ms | Throughput and RTT FAIL. |
| RC7 RC2-growth + RC6-batch (4) | 56.78 | 25.75 | 973ms | Both FAIL. |
| RC7 RC2-growth + RC6-batch (32) | 52.91 | 26.82 | 670ms | Throughput FAIL. |
| RC7 RC6-growth + RC2-batch (4) | 56.09 | 25.34 | 599ms | Throughput FAIL. |
| RC7 RC6-growth + RC2-batch (32) | 52.87 | 26.81 | 763ms | Both FAIL. |
| RC7 RC6 policy (4) | 56.27 | 25.03 | 688ms | Throughput FAIL; 29/30 overlapped probes. |
| RC7 RC6 policy (32) | 52.51 | 27.44 | 771ms | Both FAIL. |
| RC7 bulk-only batching interval 35ms (4) | 55.40 | 26.11 | 876ms | Both FAIL, no material advantage. |
| RC7 bulk-only batching interval 35ms (32) | 53.59 | 26.69 | 723ms | Throughput FAIL. |
| RC7 bulk target 336→384KiB (4 and 32) | — | — | — | Safety envelope test passes, but **both speed fixtures fail before measurement** with same-socket recovery `read EOF`; reject. |

**Avoid test confusion:** the first staged fixture started at 150Mbps and abruptly downgraded to 10Mbps, incorrectly called a speedup. Subsequent `ITOM_LOAD_PROVIDER_PROFILE_EARLY=1` fixture starts at the initial low rate **before** opening application sockets. Separate these workloads forever. Also preserve `set -euo pipefail`: a previous `go test | tee` command showed a misleading green CI despite failing RTT acceptance.

## Evidence and archived runs

- RC5 relevant pre-fix commit: `004f58e85453dff595c17701e104a5fa7ac3f2e2`, branch `codex/v1.0.0-rc5`; delivery core constant 16MiB. The 16-server/12-client observed patch must be reconstructed.
- RC6 official release: https://github.com/Septonious/itom/releases/tag/v1.0.0-rc6 (test commit `497947ac6bd541b46f3184ebcb5accb755d4ce86`).
- Failed strict FieldFix1: https://github.com/Septonious/itom/actions/runs/39081003277
- FieldFix2 steady and failed staged: https://github.com/Septonious/itom/actions/runs/39081502610 and https://github.com/Septonious/itom/actions/runs/39082475113
- RC7 conservative baseline failure: https://github.com/Septonious/itom/actions/runs/39085305297
- Differential: https://github.com/Septonious/itom/actions/runs/39085544955 and https://github.com/Septonious/itom/actions/runs/39085551580
- Batching interval: https://github.com/Septonious/itom/actions/runs/39085844605 and https://github.com/Septonious/itom/actions/runs/39085852813
- 384KiB pre-measurement failure: https://github.com/Septonious/itom/actions/runs/39086640776
- Android+server field logs, 2026-10-17 23:05:45–23:08:20 UTC: all ten accounts authenticated, but massive application TCP closures during speed-test acceleration and short-traffic concentration on `a03` (291 of 464 fast physical DATA batches).

## Next RC decision

Restore **RC5 16MiB server / 12MiB client** ACK growth and BODY service policy while keeping RC6+ recovery, quota and diagnostic improvements. Encode separate hard ceilings for both delivery policy probing and fresh path discovery; verify side mapping and migration. **Do not call it the exact original experiment** without the original patch. Run full correctness, 4/32-stream strict 75/75 ≤750ms, low-speed ramp, fresh/retained and live Android validation. If any fail, keep it **test-only** and do not publish tag `v1.0.0-rc7`.


## Follow-up October 18 2026: reproducible A/B after 16/12 restore

All variants below retained exactly `org.itom.client` source identity, the authenticated 16MiB server / 12MiB client maximum, and the strict 75/75 Mbps, max 750ms qualification without any tolerance or fabricated results. Each entry links an independent, full 60-second 10-account laboratory run.

| Experimental variant | 4-stream Mbps up/down, worst RTT | 32-stream Mbps up/down, worst RTT | Low-link result | Decision |
|---|---|---|---|---|
| Cold initial window **128KiB**, original 336KiB batch | 151.690/76.183, 447ms; trailing floor 72.378 | **131.640/79.832, 428ms**, verified tail 78.014 | True 10Mbps startup timed out; 20→50→150 reached 1141ms | **Reject**: 4-stream trailing floor and low-link RTT |
| Cold initial window **256KiB**, original batch | 157.933/77.472, 431ms; trailing floor 73.995 | **123.404/80.806, 405ms**, verified tail 79.561 | 10Mbps timeout; 20→50→150 1430ms | **Reject**: 4-stream trailing and low-link |
| 128KiB seed + **352KiB** batch target (physical 512KiB hard cap unchanged) | 157.706/76.773, 359ms; tail floor 72.955 | **135.000/79.386, 365ms**, tail 77.813 | 10Mbps timed out; 20→50→150 1356ms | **Reject**: no sustained tail or RTT improvement |
| 128KiB seed + one-second fast-placement balancing | 153.695/76.900, 421ms; tail floor 74.396 | **127.459/78.193, 371ms**, tail 76.389 | 10Mbps timed out; 20→50→150 1163ms | **Reject**: 4-stream tail still <75, low-link RTT |
| 128KiB seed + **10** bulk scheduler workers (from 8), same ten accounts | 155.591/75.340, 517ms; tail floor **70.648** | 112.237/73.335, 615ms; tail floor **59.900** | 10Mbps 1342ms; 20Mbps probe timeout | **Reject**: worsened both sustained speed and stability |

GitHub raw CI:
- Initial windows: https://github.com/Septonious/itom/actions/runs/39121098217 (128KiB), https://github.com/Septonious/itom/actions/runs/39121100927 (256KiB)
- 352KiB batch: https://github.com/Septonious/itom/actions/runs/39121472631
- Short-placement: https://github.com/Septonious/itom/actions/runs/39121668904
- Ten writers: https://github.com/Septonious/itom/actions/runs/39121764599

Grant-readiness regression: original `TestFieldLegacyGrantPublicationDoesNotStrandThreeAccounts` intermittently asserts that *both grant queues are nonempty at the very instant a ready carrier is inspected*. Production readiness only attests both grant classes were published, and active DATA workers can consume them immediately. A test-only **bounded 5s refill plus original account-specific TCP echo** passed **six consecutive local repetitions**, but this does *not* qualify production refill without full CI. No production change or test loophole has been promoted.

**Current engineering disposition:** original tested 16/12MiB candidate `2b08127305530b2e16ee3c05f669ad8331a6d559` remains the best reproducible source. Its matched APK, Linux AMD64/ARM64 binaries, source and checksums are built and can be supplied **for controlled tests only**. Full strict fresh+retained 12-case throughput and 10/20Mbps stability still FAIL. **Do not publish `v1.0.0-rc7` as a qualified release.**
