# DEV7 laboratory results

Exact runtime: `d4cc33c4db2277be8b93e48a7e3b715032a38b00`.
Main comparison: https://github.com/Septonious/itom/actions/runs/39946916440
Captured-capacity comparison: https://github.com/Septonious/itom/actions/runs/39946916484
All 15 source/fixture manifests were checked against the exact Git objects. RC2 and DEV6 production sources were unchanged. Thresholds were not relaxed.

Original sustained duplex gate: **DEV7 17/20, RC2 19/20**. DEV7's misses were two loaded RTT maxima (811.09 and 1058.65 ms versus the original 750 ms limit) and one 99.5622912 Mbps verified tail versus the 100 Mbps floor. The minimum requested 17/20 is met; every miss remains a failure.

Original short sequences: **DEV7 48/48, RC2 42/48, DEV4 42/48**. All phases contain real public-supervisor TCP transfers, byte integrity, startup deadlines and half-second byte clocks.

Mean useful rates from the same sustained rounds (full duplex; failures remain included):

| Profile | RC2 download/upload Mbps | DEV7 download/upload Mbps |
| --- | ---: | ---: |
| mobile-variable | 248.8 / 116.9 | 263.2 / 114.4 |
| multi | 229.2 / 166.0 | 233.9 / 151.5 |
| retained-multi | 197.5 / 143.2 | 232.7 / 133.7 |
| retained-single | 228.0 / 126.8 | 251.1 / 142.5 |
| single | 250.5 / 183.8 | 290.6 / 167.0 |

Some full-duplex profiles have up to 9.2% lower mean upload than RC2 while download increases. The captured-capacity comparison below improves both directions versus DEV6; the candidate does not dominate RC2 on every metric.

## All-account captured-capacity sensitivity

Healthy server route 20 ms, client route 40 ms; 500/1000 Mbps client/server paths. Fifteen accounts use the captured old byte/ordinal reservation shapes and two client/three server sockets each. Three download/upload pairs run without warmup. Mean rates do not imply that every phase passed.

| Mode | Runtime | Mean download Mbps | Mean upload Mbps | Strict phases passed | Empty half-seconds after startup |
| --- | --- | ---: | ---: | ---: | ---: |
| SINGLE | RC2 | 73.6 | 24.4 | 0/6 | 15 |
| SINGLE | DEV6 | 91.9 | 69.4 | 0/6 | 14 |
| SINGLE | DEV7 | 116.8 | 104.1 | 2/6 | 6 |
| MULTI | RC2 | 75.6 | 21.6 | 0/6 | 16 |
| MULTI | DEV6 | 92.7 | 68.7 | 0/6 | 14 |
| MULTI | DEV7 | 119.1 | 91.3 | 2/6 | 4 |

All twelve DEV7 phases completed, but only **4/12** satisfied every strict criterion, versus **0/12** on either baseline. The phase-level failures, byte windows and exact source manifests are retained. DEV7 improves mean throughput in both directions and reduces stalled windows in both modes relative to DEV6. It does not eliminate every stall or guarantee 100 Mbps in every phase.

These are capacity sensitivity tests, not a calibrated replay of Rambler, Android CPU or server storage service. One historical grant models each captured total; the capture does not include the original complete journal records. The earlier high-rate tests on empty or less-indebted journals were a different workload and could not predict this installed-state performance. No real-provider 100 Mbps restoration or field idle RTT improvement is claimed.

## Correctness and race findings

The original mixed-load race test exposed an actual shared diagnostic snapshot race before the final source. Another worker could re-plan a queued waiter while its diagnostic struct was copied after unlocking. DEV7 takes that copy under the existing mutex; delivery/credit policy is unchanged. The new 64-worker mixed-admission regression reproduced the race on DEV6 and passed ten local repetitions after the fix. The exact-source CI run passes the original mixed-load race test, and packaging rejects any detected data race. A local candidate run still missed that test's original 1100 ms loaded-chat deadline without a data race; that result is retained rather than recast as a pass.

The build manifest separately records complete correctness, accounting/mailbox races, Android tests/lint, native hashes, package identity and signing verification. Qualification remains **experimental** because the performance failures and real-provider verification are outstanding.
