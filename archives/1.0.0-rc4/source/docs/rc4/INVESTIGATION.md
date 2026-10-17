# RC4 pressure-aware response polling

## Evidence and limits

The supplied captures identify RC3. Client retained trace: 2026-10-17 00:51:11–00:52:33 UTC; server: 00:50:39–00:52:47 UTC. The client ends CONNECTED with 20/20 lines and no current error. No failed IMAP command or new reader repair appears inside the retained client ring. The failed Speedtest and initial correlated socket closures cannot be reconstructed fully from the remaining window.

The retained previous-generation socket snapshots preserve read/eof as the first close cause across the original control/DATA receive sockets. The current per-account reader repair counts are one for nine accounts and two for the tenth; its last receive generation closed receive/no_progress after 8000 ms. Logical Reconnects remain zero. This supports physical socket loss/repair history, not a claim that the logical session restarted, nor proof that polling caused the upstream EOF. Account maximum operation and control queue waits reach multiple seconds, mostly outside the retained event window.

161 complete echo pairs have min 176.07 ms, median 285.81 ms, p95 508.85 ms and max 916.03 ms. Compared with the previous RC2 capture's median 345.83 ms, RTT improved, but these runs have different workload/timing and do not establish an isolated causal experiment. Client PING queue median remains 1.62 ms; server turnaround median is 1.65 ms. The user's screenshots show 39.6–89.7 Mbps down, 31.0–32.1 Mbps up, one failed run, and reported reconnects. The input therefore fails the no-speed-regression field criterion despite improved latency.

## Correction

RC3 applied 10/20/20/... ms pacing to every hot reader. Hot can be caused by a pool-wide parallel-setup hint or periodic per-account probes, and requests can renew windows. Bounding each hint's lifetime did not bound simultaneous accelerated readers. RC3 also saturated the shared empty streak at two rather than four, altering the subsequent quiet transition. Both are corrected.

The default hot sequence and streak are restored exactly to RC2: 10/20/40/80/80... ms with saturation at four. The 20 ms cap is now advisory only for a direct, unexpired local response hint, on at most the two existing quiet reserves, with no outstanding bulk bytes, no queued non-fast work, and no more than two outstanding short ranges. Parallel setup, bulk, non-reserved readers and missing reserve capacity use RC2 pacing. Accelerating a turn never rewrites the shared idle history: quiet transition still reaches the original two-second cap. Actual visible backlog drains immediately in either mode. Existing readWake interrupts remain effective.

No extra connections, login loops, journal credit, congestion/window relaxation, timer refunds or retry authority are introduced. RC2 stalled-account routing, authenticated early-NACK retention and bounded receive repair remain intact. The two-reader decision is advisory under the existing group lock; bulk arriving just after it is checked can overlap one already selected turn, then the next iteration observes the new state.

## Validation scope

A before-fix test reproduces the RC3 pressure and quiet-transition contract regression. Focused race checks cover default pacing, reserve count, queued/active bulk exclusion, parallel short exclusion, light-demand resumption, quiet/backlog behavior, reader ownership/repair, prior routing and NACK rules. Normal full-supervisor checks exercise twelve concurrent streams through a temporary pause, persistent blackhole repair, correlated control EOF, original messaging and new post-load connections. These are finite integrity/recovery checks, not a throughput or provider-throttling certificate.

RC4 is an unstable testing candidate. Confirm restored sustained throughput, no failed MULTI run and no recurrent physical socket wave on the same real route. Sub-200 ms is not a release guarantee; stability and useful throughput take priority. The historical RC2 instrumented startup timeout remains unresolved. No causal upstream EOF fix or blanket mobile-crash resolution is claimed. Raw user captures and credentials remain private.
