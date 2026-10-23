# Field-driven review from RC2 through DEV8

The accompanying evidence index inventories89 distinct canonical archived reports (91 paths, about5.9MB) through commit0df6350, including previous source evaluations and complete raw result JSONs. Inherited copies of old FIELD documents inside every source snapshot are excluded from that count. This review follows the user reports as external validation; it does not rerun every historical binary or pretend that a synthetic rate measures Rambler. Existing source snapshots, rejected patches and failed results remain preserved.

| Stage | What tests established | What field results established | Decision for this rework |
| --- | --- | --- | --- |
| RC2 / final1.0.0 | Original durable accounting and transport baseline; some retained/high-RTT synthetic cases still fail | User's last consistently usable baseline; earlier real tests reached roughly90–100Mbps | Keep as immutable comparator; stable1.0.0 unchanged |
| RC3–RC4 | Some latency improvements in limited models | Reduced rates, failures and reconnects | Reject latency-at-any-cost window cuts |
| RC5–RC9 | Faster growth, smaller windows, placement and batching trials; 4MiB flight around41/32Mbps and rate-admission around19/18 failed original floors | No consistent simultaneous RTT/throughput/stability advantage | Do not resurrect fixed small flight caps or global read gates |
| RC10 | Corrected healthy20ms server model, paired fresh/retained and low-client-capacity cases; mixed outcomes | Failure and rate-tail complaints continued | Keep independent endpoint shaping; reject a universal improvement claim |
| RC10.1 | Finer stage profiler | Roughly5–21Mbps MULTI and14Mbps SINGLE download; idle363–379ms in reported successful screenshots | Retain causal stage measurement, not profiler output as proof of speed |
| RC11–RC13 | Journal no-op/proof, retained-grant and severe-reserve fixes; RC13 restored20/20 directions for an exact14/20 case | Immediate Speedtest failures, upload decay, lost-wallet reservations after reinstalls | Reuse only individually proved authority fixes; never erase old charged state |
| 1.1 DEV1–DEV2 | Low5ms-client mean RTT improved; capped recovery or40ms tails regressed; retained upload stayed~77Mbps | No established universal field advantage | Keep capped repair regressions and realistic client40–180ms paths |
| DEV3 | Useful-delivery model separates scarce mailbox supply from network congestion; bulk full-flight trials sometimes improve | No proof this alone lowers provider command turns | Keep bulk demand/ACK authority; attack physical scheduling independently |
| DEV4 |20/20 original rounds; retained one-way upload~158Mbps, but simultaneous download8–13% lower | MULTI mean66.7/33.4, SINGLE13.1/68.0; idle~353–356ms, startup/ramp failures | Long warmed duplex counts are insufficient; retain cold TLS and short sequential tests |
| DEV5 |48/48 short phases versus DEV4's42/48; shared socket fairness and recovery audit pacing | Incomplete line readiness and poorer rates | Keep fairness/bounded audits; add actual historical byte pressure |
| DEV6 | Exact byte-pressure shapes restore30/30, atomic prepare/close preserved |~23–26Mbps down/~20–25up;~523–529ms idle | Readiness is separate from sustained service; keep debt across trials |
| DEV7 | Bounded live tails, fewer cleanup command turns, diagnostic race fix |23.3/71.2 one success, three reported failures;416ms idle | Keep proven race/discovery corrections; add short-start failure gates |
| DEV8 |20/20 original rounds and48/48 short phases;12 stale-UID/IDLE/index cases red→green | Latest28/30,~31–33 down/~20–24up;~431–517ms idle, half tests fail | Original broad lab counts cannot certify deployment; use captured topology/debt and retain every closer failure |

The complete RC3–RC13 per-file decisions are preserved in `archives/post-rc2/POST_RC2_EVALUATION.md` and `SOURCE_EVALUATION.json` (available through Git even in a sparse checkout). No later transport change was promoted into stable final1.0.0. All DEV1–DEV8 exact builds and selected/rejected comparisons remain under `archives/1.1.0`.

## Errors in the inference chain

1. The original high-rate fixture has more sockets, generous remaining quota, long measurements and little backend/durable storage cost. Its200–350Mbps numbers are verified simulator application-byte rates, not predictions for a two-socket phone with dozens of old grants per account.
2. Full qualification comparisons fluctuate even on unchanged RC2 (for example16/20 versus13/20 in different cohorts). Compare on the same runner and preserve every round; choosing an older favorable control is not a causal result.
3. Wrapper FETCH/APPEND times include queueing. Applying their full duration as backend delay double-counts contention. The provider/commit sensitivities must identify their assumptions and keep raw20ms server propagation separate.
4. Capped readiness alone is not a Speedtest reliability test. TLS handshakes, response headers, first-ready-account load, aborts, first/last two seconds and500ms useful-byte windows must also be checked.
5. Timeout samples are censored observations. Late replies on a timed-out TCP stream cannot be treated as corruption of all later requests. A new stream is needed after preserving the original miss.
6. Split layouts were excluded from the quiet-reader selector. Changing its score did not test the intended split reserve. The new tests must assert the selected physical role rather than infer it from a policy name.
7. Repeated app resets change the installed capacity state. Missing wallet history cannot be substituted with zero-spend reports. A new controller must operate on truly remaining capacity and report when safe service is mathematically impossible.

A new RC must show concrete startup/readiness and application results against both DEV8 and immutable RC2, with correctness/race safety and original thresholds. Any remaining failures must remain visible; a broad pass cannot override a closer failed workload. User confirmation on real Android/Rambler remains the final field validation, not something a mock can manufacture.
