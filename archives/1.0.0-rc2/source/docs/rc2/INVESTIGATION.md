# RC2 field investigation

The paired latest captures identify 1.0.0 RC1. The retained client trace spans 2026-10-16 23:47:34–23:49:10 UTC; the server spans 23:47:37–23:49:27 UTC. Ring rotation removed earlier startup history, so the initially failed MULTI attempts cannot be assigned a cause from these files. The screenshots are individual field measurements, not synchronized benchmark samples or a sustained 100 Mbps certificate.

## Observations

One account experiences concurrent long IMAP commands on both endpoints near 23:48:20–36 UTC. Client maxima: control APPEND 10.17 s, presence 10.62 s, control FETCH 4.41 s, DATA FETCH 8.10 s. Server maxima on the same account: maintenance FETCH 15.96 s, control APPEND 10.61 s, control FETCH 10.26 s, DATA APPEND 9.95 s. A client DATA reader retires with TIMEOUT and its first replacement connection fails; the subsequent replacement succeeds. This is a localized provider/backend or path delay; these traces cannot identify its external cause. There is no captured wave of carrier-wide reconnects.

Of 189 complete paired control echoes, median round trip is 332.2 ms, p95 586.6 ms and maximum 13.02 s. The extreme sample includes 11.50 s between server PONG sealing and client receipt. Later PINGs queue locally for 7.14 s and 2.19 s on that account. Across all accounts the ordinary local PING queue median is 1.55 ms, p95 58.64 ms. Cross-endpoint components are approximate and depend on clock agreement; the client round trips and local command durations do not. These echoes are distinct from Speedtest jitter.

Ordinary physical APPEND throughput is not the dominant retained wait: DATA batch completion median/p95 is 64.7/164.1 ms client and 124.5/239.2 ms server. Client stripe placement p95 is 0.2 ms, server 74.0 ms. Authenticated grant rollover with WALLET_EXHAUSTED is normal bounded accounting and must not be treated as account corruption.

## Reproduced software defect and correction

Bulk cumulative feedback keeps a stable account affinity to coalesce receipts and WINDOW updates. Its candidate eligibility looked at queue/debt bounds but not elapsed in-flight control work. An APPEND blocked for ten seconds could therefore retain feedback affinity while healthy authenticated siblings were available. OPEN ranking similarly omitted the elapsed in-flight writer. Two small regression tests fail before the routing correction and pass after it.

Track the outer control writer start across recursive control batches. Only an active writer exceeding max(1 second, min(3 seconds, 250 ms + twice unloaded echo RTT)) receives a bounded routing penalty. Completed and quiet writers incur none. Bulk feedback selects responsive primary candidates before preserving affinity; if every path is delayed, existing last-resort routing remains available. New OPEN, short DATA and bulk placement include the same cost. Initial WINDOW affinity cannot override a detected stall. Ordinary loaded RTT does not replace the propagation baseline.

This is advisory placement only. No writer is canceled, socket reconnected, frame replayed, readiness fabricated, or durable debt released by the timer. Already queued account-bound controls and data cannot safely be reassigned/replayed. Authenticated receipts, grants, immutable repairs and deletion proofs retain their existing rules. RC1 early-NACK and bounded receive-stall recovery remain unchanged.

## Limits

This change reduces exposure of new logical traffic to an observed account stall. It cannot eliminate provider stalls, guarantee every Speedtest server accepts every test, or certify a jitter ceiling from old screenshots. Do not claim the initial MULTI failures reproduced: their trace history was rotated out. No additional throughput/window/login tuning is justified by these retained samples. Qualification results and exact scope are recorded with the packaged artifacts; stable promotion still requires all internal gates and real-device/provider follow-up.

Raw user captures, profiles and credentials remain private.

## Build identity correction

The first RC2 Android build passed 82 domain and 7 app unit tests plus lint, but the mandatory post-build native identity gate rejected the leftover RC1 TransportRevision constant. It was corrected before any publication, and the offline identity check now explicitly validates core/tunnel/version.go too. The mismatched source-fixture regression was extended to prevent this omission. The final targeted checks and builds are rerun on the corrected source; the superseded failed build report is retained as clearly labeled historical evidence, not treated as a final build result.
