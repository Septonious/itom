# ITOM 1.1.0 DEV1: current laboratory result

Candidate source: [803d1fa](https://github.com/Septonious/itom/commit/803d1fa63029c1b0eaf5f4ebca7f47bad0028b02). [Matched run39599718710](https://github.com/Septonious/itom/actions/runs/39599718710). This is development evidence, not a stable release or physical Android/Rambler qualification. No acceptance threshold is reduced.

Only the demonstrated low-route receive search and per-socket quiet/burst coalescing are applied. Slower/unknown direct provider paths retain RC2 receive timing. Grant/journal/wallet/wire/quota/recovery code remains the frozen RC2 contract; fast rollover is deferred as a separate patch. Final1.0.0 and preserved post-RC2 archives are unchanged.

## Idle and light application RTT

Ten accounts, independent2ms server/1000Mbps and client500Mbps routes, actual production supervisors, TLS/IMAP, journals and persistent verified TCP echoes. Each route uses three alternating AB/BA/AB pairs and72 verified32-byte requests per phase/version. Light load adds a4KiB echo stream; it is closed-loop, not a fixed offered-rate benchmark. Times below are pooled mean/p95 milliseconds; independent experimental units are the three runs, not72 unrelated machines.

| Client route | RC2 idle | DEV1 idle | RC2 light | DEV1 light |
| --- | ---: | ---: | ---: | ---: |
| 5ms | 49.7 /75.4 | 36.6 /66.9 | 51.2 /111.1 | 36.6 /62.9 |
| 40ms | 96.7 /139.5 | 92.8 /109.8 | 103.5 /143.0 | 96.5 /133.0 |
| 100ms | 189.1 /262.1 | 189.0 /243.7 | 195.8 /309.8 | 175.7 /223.3 |

All latency invocations completed, keeping their original two application sockets and carrier identities. Not every extreme improved:40ms idle max195.5→198.0ms and light172.3→206.0ms. That small sample cannot establish universal tail non-regression. Earlier broader policies produced worse40ms distributions and were not accepted; both prior reports and failures remain recorded.

## Sustained paired transfer

Two complete60s rounds per side/scenario, client40ms/500Mbps, server2ms/1000Mbps except the explicit20ms sensitivity case. Same byte generators, complete scheduled probes, original full/30s/10s tails, all sockets and100Mbps/750ms gates. Each scenario runs both binaries serially on the same four-core shared runner. Rates below average only complete rounds, upload/download Mbps.

| Scenario | RC2 | DEV1 | Result |
| --- | ---: | ---: | --- |
| Fresh SINGLE | 160.8 /308.6 | 164.1 /315.9 | Both PASS |
| Fresh four streams | 159.6 /301.6 | 160.4 /298.2 | Both PASS |
| Fresh MULTI32 | 153.3 /269.3 | 153.2 /270.0 | Both PASS |
| BINARY/LITERAL+ four | 240.0 /259.6 | 242.0 /251.9 | Both PASS |
| Older20ms server route | 183.0 /241.8 | 181.4 /241.9 | Both PASS |
| Retained SINGLE | 76.6 /308.2 | 76.8 /309.4 | Both FAIL100Mbps upload/tail target |
| Retained MULTI32 | 76.8 /286.3 | 76.8 /282.6 | Both FAIL100Mbps upload/tail target |

All28 measurement rounds completed,420 loaded probes per version, with no timeouts/missed probes or carrier/socket replacement. Candidate maximum loaded RTT was340ms; RC2344ms. The observed BINARY download difference is retained in the table; two rounds do not certify equivalence or a causal regression. Earlier matched runs varied in both directions. This evidence supports continued field evaluation, not an all-scenarios100Mbps claim. Retained-state failures still block stable qualification.

## Other checks

The supplied worst4G trace,1Mbps down/2.5Mbps up and2ms server route completed78 verified16KiB request/replies in30.276s:0.338Mbps each direction, above the separate0.1Mbps connectivity objective. This does not waive any high-capacity target.

Corrected mailbox watchdog checks and journal/wallet/flow/datapath race checks passed locally. An extra local race-instrumented HTTPS stress invocation missed its original5s HTTP deadline without a race detector report; the same functional workload passed in a normal matched local run for RC2 and the preceding equivalent low-route candidate. This extra failure is not hidden or called a pass. Full CI correctness, Android SDK tests/lint, native identity and artifact status are recorded by the linked workflow when completed.

Exact rounds, raw latency samples, all acceptance decisions and raw-log SHA256 hashes are in [experiments/ci3-results.json](experiments/ci3-results.json). Runtime measurements are laboratory inputs; a2ms server ICMP ping does not establish provider APPEND/index/notification latency. Real Android/Rambler validation on the new server is still required.

## Full correctness outcome for this DEV1 checkpoint

The full suite later failed the original capped/shared-socket repair test (12s1MiB transfer after forced control EOF). APK/binary export for this checkpoint was skipped automatically. The next DEV2 candidate preserves RC2 wait timing whenever DATA/maintenance are not physically isolated, and repeats correctness/qualification. These DEV1 performance numbers do not certify that later candidate.
