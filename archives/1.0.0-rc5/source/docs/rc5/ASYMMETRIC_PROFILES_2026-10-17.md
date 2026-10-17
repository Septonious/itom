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
