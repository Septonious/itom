# Rev1 field failure: numeric observations

Date: 2026-10-14. Both archives identify 0.9.2-Rev1. The retained trace windows overlap for roughly 106 seconds and include 15 active account identifiers. Raw uploaded traces remain private and are excluded from release artifacts.

The three supplied Speedtest screenshots report 7.87/36.0, 1.75/0.27 and 7.79/20.6 Mbps download/upload. Idle/loaded-download/loaded-upload RTT values are respectively 517/1144/587, 392/1305/571 and 619/804/617 ms, with jitter 214, 190 and 870 ms. Test mode, matched account-count baseline and exact run mapping are not recorded in the screenshots.

| Direction | Bulk body size median | Bulk completion median | Bulk completion p95 | Control APPEND count | DATA APPEND count |
|---|---:|---:|---:|---:|---:|
| S2C | 32972 bytes | 156.2 ms | 425.8 ms | 4802 | 1368 |
| C2S | 65920 bytes | 75.6 ms | 141.9 ms | 5731 | 1134 |

These body sizes include framing/encryption. They are not useful application throughput. S2C typically publishes one 32 KiB record per bulk mail, while C2S median is about two records. Server bulk completion median is over twice client bulk completion median. Server pre/post-APPEND journal stages and the repeated control/cleanup work contribute overhead. The DATA receiver was not fully traced in Rev1, so the missing command measurements cannot establish its actual service time.

The controller snapshots show a 64 KiB upload flight window persisting through idle periods; S2C download begins its observed bulk interval with about 128 KiB and only reaches about 674 KiB during the test. More account sockets do not compensate for a shared logical flight window or tiny physical batches.

Two sufficient controller defects were reproduced in fresh deterministic Go regressions: drained congestion retains a 64 KiB seed for the next phase, and new bulk retroactively relabels earlier short traffic as loaded pressure. A fresh mobile epoch also loses its 256 KiB cold-path seed. The three reproduction cases fail on Rev1; the actual bulk-exposed-short-pressure protection case passes. Rev2 fixes the reproduced defects and retains fixed 800/650 ms bulk/short objectives.

Matched, sampled S2C seal-to-receive events have median 226.0 ms and p95 377.3 ms, but cross-host clock offset is not calibrated and DATA frame tracing is sampled; these are diagnostic observations, not application RTT.

## What the 15-account evidence can establish

All 15 carriers were active. The code has eight bulk pump writers per logical session, so fifteen accounts do not yield fifteen simultaneous logical writers. Additional accounts can supply quota headroom and alternatives, but also add readers, polling, liveness, durable controls and cleanup. There is no matched 10-account run in these archives, so a causal 15-versus-10 gain or loss cannot be calculated from these captures alone. Rev2 will compare 5, 8, 10 and 15 accounts under declared field-derived stress assumptions and report actual pass/fail/throughput results. Such local comparisons remain laboratory evidence, not a provider-backed account-count benchmark.
