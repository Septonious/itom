# DEV6 final-source laboratory results

Runtime source: `765643ea2bf0a0035023f1376ed136223f52ba3c`.
[Complete round/phase record](RESULTS.json), [direct DEV5 comparison](DEV5_COMPARISON.json), [field analysis and fixes](FIELD_ANALYSIS.md).

## Qualification comparison

The final-source comparison is [run 39882769996](https://github.com/Septonious/itom/actions/runs/39882769996). All twenty sustained rounds and all forty-eight short phases per variant were observed. The original 100 Mbps minimum useful/full/tail bandwidth and 750 ms maximum duplex RTT gates remain unchanged. Individual comparator failures make several matrix jobs red; they are retained.

| Check | DEV6 | Frozen RC2 | Frozen DEV4 |
| --- | ---: | ---: | ---: |
| Sustained duplex qualification rounds | 19/20 | 19/20 | not run |
| Three consecutive short download/upload sequences across eight fixtures (phases) | 48/48 | 42/48 | 41/48 |

DEV6's one sustained miss is variable-mobile trial 2, round 2: minimum verified tail **99.457434 Mbps**, below the unchanged 100 Mbps floor; maximum measured probe RTT **625.359080 ms**, below 750 ms. It is a failure, even though the full-round rate passed. No round was dropped, repeated to replace a failure, or waived. RC2 also missed one round. This is an experimental testing RC, not 20/20 qualification and not evidence that real-provider idle RTT is fixed.

Thirteen provenance manifests (eight short, five duplex) were checked against the final production files and the frozen comparator sources. The laboratory provider implements real TLS/IMAP and durable fsync but is not Rambler and does not reproduce its backend queueing or account shaping. Reported rates above 200 Mbps are useful TCP bytes in those fixtures; they do not predict field throughput.

## Direct comparison with the previous DEV5

[Run 39882813844](https://github.com/Septonious/itom/actions/runs/39882813844) freezes DEV5 `3ccdd5a631ddf33c3d764fda1b6e52b4ad278b3a` and DEV6 `765643e`. Each fixture uses ABBA order DEV5, DEV6, DEV6, DEV5, identical shared tests and complete consecutive phases. Both variants pass **36/36** phases.

| Fixture | Download DEV5 → DEV6, Mbps | Upload DEV5 → DEV6, Mbps |
| --- | ---: | ---: |
| shared SINGLE | 203.005 → 200.955 (−1.0%) | 120.433 → 121.129 (+0.6%) |
| shared MULTI | 224.064 → 232.930 (+4.0%) | 118.525 → 115.303 (−2.7%) |
| field socket budget: client 2, server 3 | 316.579 → 320.158 (+1.1%) | 215.366 → 227.585 (+5.7%) |

These observed differences span −2.7% to +5.7%. They do not establish universal speed gains or a formal noninferiority margin. The specific improvement over DEV5 is quota-bound startup/refill: all three new field-shaped debt regressions fail on DEV5 and pass on DEV6 without forgiving historical grants.

## Fifteen-account startup and worst raw 4G

With historical byte pressure on every account, sparse UID history, fresh client wallets and five total sockets per account, the real TLS/IMAP test reaches **30/30 active directions** and verifies short/bulk/short traffic on each of fifteen accounts. It separately times actual client attempts and UID fixture seeding, and checks that every old grant remains unchanged. Healthy-server tests fix the server network path at **20 ms** and compare client 40 ms with variable 40–180 ms access latency. The final-source run measures maximum client-attempt readiness at **2.600 seconds** for fixed 40 ms and **4.972 seconds** for the variable profile. The separate 66.3/68.7-second fixture setup includes UID seeding and is not login latency. This reproduces sufficient debt shapes, not the exact missing field journals.

The final source also passes the raw-4G fallback case: client download/upload limits **1/2.5 Mbps**, client RTT trace **84, 63, 43, 67, 23, 60, 55, 56, 21, 66 ms**, server path 20 ms. It delivers **0.287203 Mbps in each direction**, **66 completed exchanges**, **zero failed**, over 30.121 measured seconds. The target is at least 0.1 Mbps for text connectivity.

## Retained failures and limits

Before DATA fallback preflight repair, the existing capped-control EOF test failed on both DEV5 (1/10) and a preliminary DEV6. The final repair passes its deterministic known-dead case, preserves unknown-publication debt, and never replays an actual APPEND. Isolated original-deadline stress passes 20/20. A separate run concurrent with race compilation still fails **6/30**, retained as unresolved CPU-contention sensitivity.

On final source, the first CI run passes the full normal Go suite (tunnel 735.857 seconds), vet, ten additional capped-EOF repetitions and journal/wallet/flow/datapath race tests. Its combined tunnel race process later misses the existing 1100 ms chat deadline during mixed bulk ramp-up. A fresh standalone instrumented run also reproduces that deadline miss on DEV5. These are timed-test failures, not reported data races; neither is relabeled a pass. Further build checks isolate every originally selected race test in a fresh process with its original payloads, deadlines and assertions. The isolated CI process also misses the same 1100 ms mixed-load chat deadline. All other selected race checks pass without reported data races. An unsuccessful dynamic split-control reserve experiment also times out and was discarded; production remains byte-identical to 765643e. Those results and failures are recorded in DEV6_BUILD.json. No assertion or qualification threshold was changed.

Testing packages require the full normal suite, vet, accounting races, quota regressions and all remaining tunnel race checks. The known mixed-load timing diagnostic remains FAILED and visible; this explicit experimental packaging exception does not turn it into a pass or make the release fully qualified. It is permitted only for that exact chat deadline failure, with no data race, panic, bulk request failure or other failed test. The original sustained comparison remains 19/20, with its one tail miss retained.

The field screenshots still show idle ping 424–537 ms and useful rates 14.1–26.6 Mbps. This release targets the proven allocation/readiness and dead-socket problems. It does not claim that all idle spikes, field Speedtest failures or provider shaping have been eliminated. Preserve the Android wallet and server journals when testing.
