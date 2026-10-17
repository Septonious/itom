# RC5 physical DATA→ACK timing and failed recovery experiment — 2026-10-17

**Status:** Experimental, not deployable. The mandatory full 10-account 100/100 Mbps duplex qualification is still FAILED. No tag, release, profile migration, journal forgiveness, runtime recovery override or APK was published.

## Proof-bearing telemetry now in RC5

`deliveryController.progress` records `lastBulkACKAgeMS` and `lastBulkACKAt` only after a definitively accepted original physical DATA attempt is covered by a genuine authenticated cumulative logical ACK. An unaccepted APPEND, a repaired range or absence of verified progress produces no sample. The existing short-range ACK age remains independent. The diagnostic `latencyGovernorSnapshot` exposes last confirmed physical-to-ACK age and its freshness, and the optional `ITOM_LOAD_DUPLEX_TIMELINE=1` lab logger correlates client/server acknowledgment age with 5-second useful throughput and logical window. These counters **do not authorize admission or quota changes**. A separate `TestRC5BulkACKAgeRequiresConfirmedOriginal` regression passed on Go 1.27.1.

## Controlled baseline (server-only 24MiB / client 12MiB, fresh laboratory)

10 accounts, 24 streams per direction, 80ms modeled IMAP command cost, 600Mbps provider, 4 Go threads, 60 seconds simultaneously:

- **Download 106.011 Mbps; upload 35.761 Mbps** full interval, max loaded RTT **401ms**; all 30/30 loaded probes completed, original/fresh application sockets survived. Strict throughput FAIL.
- At t=5 seconds, client logical window 16MiB, aged outstanding bulk 2428ms, most recently confirmed physical DATA→ACK **2702ms**. At t=15 seconds, window fell to 2MiB and the worst short-range age reached 3790ms, while the server remained in NORMAL at 16MiB.
- At t=20 seconds, the client entered PROBE_UP with 3.47MiB window. Its last confirmed physical DATA→ACK was only **250ms** old (observed 99ms earlier) and the short-ACK age was 355ms, yet outstanding bulk age remained 2648ms.
- At t=30–40 seconds, the client was still probing at ~4–4.65MiB with *recent* confirmed physical-to-ACK ages 252–303ms. It nevertheless suffered further window cuts around t=45–60 seconds (1.26MiB, then below 1MiB).
- **Important:** recent healthy physical-to-ACK observations cannot prove all outstanding older ranges are healthy. The old range, fast-range pressure, and actual outstanding budget remain authoritative. This is a liveness diagnosis, not reason to suppress verified congestion.

## Two isolated controller variants rejected

1. **Suppress repeated congestion halvings while new authenticated fast and bulk ACKs continue:** 10-account full download **106.269 Mbps**, upload **39.099 Mbps**, max RTT **503ms**. Late 5-second upload windows fell to 5–11 Mbps. Strict FAIL. Original halving/RTT/outage protections remain in GitHub; the experimental FastProgress field was not promoted.
2. **Permit a larger logical-window recovery probe (window/4 rather than 64KiB) when a freshly authenticated physical DATA→ACK is <50% of the latency budget, short range age is healthy, no explicit short pressure and at least three authenticated healthy turns have occurred:** download **110.026 Mbps**, upload **26.468 Mbps**, last30s **115.317 / 16.506**, last10s **115.841 / 14.234 Mbps**; max RTT **595.973ms**, 30/30 valid probes, all six same-socket post-load checks plus a fresh 256KiB transfer passed. **Strict FAIL**. The new policy was confined to disposable local source, not committed.

Both variants preserve original physical wallet/cryptographic/IMAP receipts, but their measured speeds prove they are NOT solutions and **must not be deployed**. Better downstream peaks never count as simultaneous sustained 100/100.

## Next engineering lead

The necessary work is to explain recurring late short-range and bulk ACK cycles at the original service level, not to make the controller disregard them. The remaining instrumentation should correlate physical sender APPEND completion, receiver BODY proof/stream dispatch, ACK queue/seal/CONTROL APPEND, receiver CONTROL FETCH and authenticated frontier update with per-account timestamps. Existing per-account writer command-admission queue measurements were often near zero, and provider active-login peak was exactly 10/10 with no rejected LOGINs under the 5+5 default; neither justifies adding connections. Keep the full 12-case 10-account SINGLE/MULTI fresh/retained gate (each direction >=100 Mbps throughout full and sustained tails, loaded RTT <750ms, zero misses/timeouts, after-load survival) and separate real-provider Android validation mandatory.


## Forensic trace and rejected ordinary-STATUS fairness experiment

Paired RC5 simulator diagnostics with 24MiB server/12MiB client journals and trace enabled were correlated by Noise ciphertext counter (no payload was inspected): **6177 matched control records**, zero negative cross-endpoint timestamp intervals. Server-to-client `STATUS` and client-to-server `STATUS` physical seal→receipt p50 ~130ms, p95 ~207ms and ~187ms respectively. `MAIL_RECEIPT` and `WRITE_GRANT` physical seal→receipt p50 were ~130–135ms and p95 ~188–198ms. No paired CONTROL frame took >750ms after sealing. These values are *only for frames surviving trace rotation and matching*; enabling traces perturbs the measured useful speed (traced run download108.960/upload22.675Mbps) and cannot qualify throughput.

A separate `control_wait` trace stage showed a substantial elapsed interval **from initial enqueue token to frame sealing** on the *server*: among sampled `STATUS` events, median ~347ms, p95 ~3324ms; for `GC_STATUS`, median ~1165ms, p95 ~3912ms. **Critical caveat:** cumulative `enqueueFlowControl` / `coalesceCleanupStatus` intentionally preserve the timestamp of the first unsent token while replacing the payload with fresher authenticated control state. These long sampled queue ages are not necessarily the age of the most recent ACK content. Sampling is also biased toward waits >=100ms. Do not claim that each latest signed ACK was delayed for several seconds based on these aggregates alone.

An isolated writer fairness experiment reserved one of every four CONTROL batch positions for the ordinary coalesced `STATUS` queue while preserving the first urgent slot and receipt service. Focused ordering/receipt tests passed, but the 10-account, 24-stream, 60s server24/client12 modeled-provider case produced **download105.277Mbps / upload39.208Mbps / maxRTT526ms, 30/30 valid probes**, strict simultaneous100/100 FAIL. The change was **rejected and never committed**.

Next step: separately timestamp the **most recent cumulative STATUS replacement** and the original queue token, so control queue age can be distinguished from meaningful ACK freshness. Do not bypass journal bounds, physical proofs or controller's confirmed outage guard.
