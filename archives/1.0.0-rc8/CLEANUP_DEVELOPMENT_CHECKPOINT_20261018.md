# RC8 cleanup and descriptor development checkpoint — 2026-10-18

Status: development candidate, **not release qualified**. Continue on
`codex/v1.0.0-rc8-development`. Transport source commit: `1c43069`;
`93063e1` adds qualification automation without changing runtime source.

## Changes

- `8cc24a4`: after acknowledging an authenticated UsageReport, consult the
  durable live-attempt index before caching its descriptor. A delayed report
  for an already reclaimed ordinal previously resurrected a cache entry whose
  deleted BODY would never arrive. At the cache limit this caused
  `DESCRIPTOR_LIMIT`. The regression fails on the fetched baseline and passes
  with the fix, including a live older ordinal while a newer one is reclaimed.
  Registration acknowledgement still converges; replay returns no quota.
- `9c89675`: without usable APPENDUID hints, reclaim a useful first page of
  eligible DATA before fetching a second page. Resume the cursor next turn.
  Regression proves first-page progress, subsequent-page progress, retention
  of unacknowledged DATA, and no refund on failed physical deletion.
- `1c43069`: experimental bounded cleaner discovery increases from 2 MiB to
  4 MiB, fitting eight ordinary DATA bodies instead of four per command turn.
  This amortizes cleanup command overhead before outgoing physical headroom
  fills. Individual MIME limits, 32-UID/attempt bounds and the 16 MiB proof-cache
  bound remain. Unit coverage checks byte, duplicate UID and incarnation bounds.
  This changes read batching, **not physical mailbox quota or flight ceilings**.

No journal/wallet format, quota/refund/APPEND-uncertainty rule, account database,
TCP/session identity, recovery policy, RTT allowance or qualification threshold
changed. Android remains `org.itom.client`, versionCode `10007`, runtime revision
`itom-1-0-0-rc8`. Build both endpoints from the same source.

## Measured evidence

Same standard strict scenario: 10 accounts, 600 Mbps simulated provider,
80 ms command cost, full-duplex 60s, 2s probe cadence. These are laboratory
measurements; they do not certify an Android/Rambler path.

| Source | Streams | 60s up/down Mbps | 30s down | Minimum verified full/tail Mbps | Max RTT ms | Gate |
|---|---:|---:|---:|---:|---:|---|
| `9c89675`, 2 MiB cleaner | 4 | 158.082 / 76.804 | 74.396 | 73.531392 | 390.606 | FAIL |
| `9c89675`, 2 MiB cleaner | 32 | 131.714 / 78.556 | 79.325 | 78.555819 | 389.179 | PASS |
| `1c43069`, 4 MiB cleaner | 4 | 149.134 / 96.574 | 96.408 | 96.023347 | 416.121 | PASS |
| `1c43069`, 4 MiB cleaner | 32 | 131.299 / 94.874 | 93.848 | 90.859110 | 410.799 | PASS |

All four runs answered 30/30 probes without timeouts/misses and preserved
original sockets. Different workflow runs are not a controlled provider field
A/B. A single passing run is insufficient evidence against intermittent stalls.
Chronological RTT-difference p95 for the 4 MiB candidate was 168.457 ms (4
streams), 119.486 ms (32): the 100 ms jitter objective is **not qualified**.

- 2 MiB checkpoint CI: https://github.com/Septonious/itom/actions/runs/39141617801
- 4 MiB candidate CI: https://github.com/Septonious/itom/actions/runs/39142005201
- Matched candidate APK/Linux/source testing build:
  https://github.com/Septonious/itom/actions/runs/39142005192
- Repeated SINGLE/4/32, fresh/retained journals, two 60s rounds per case
  (12 measured cases), same strict gates:
  https://github.com/Septonious/itom/actions/runs/39142389972

Candidate `1c43069` CI passed all four jobs: vet/source/recovery, Android SDK37
tests/lint and both JNI ABIs, and strict 4/32-stream runs. Its separate matched
APK, Linux amd64/arm64 and source testing package also passed all four jobs:
[download testing artifact](https://github.com/Septonious/itom/actions/runs/39142005192/artifacts/12142311777).
Artifact expires 2026-11-17; artifact ZIP SHA-256:
`93bb39c530029deab99c2675c1d81711126bb326c857e3829c1f4979ffd2c151`.

Repeated qualification completed **9/12 PASS**. Every round completed 60s,
answered 30/30 probes without timeouts/misses, preserved original sockets and
completed post-load original/new socket checks. Three strict failures remain:

| Streams/state | Round 1: minimum full/tail Mbps / max RTT ms | Round 2: minimum full/tail Mbps / max RTT ms | Outcome |
|---|---|---|---|
| SINGLE/fresh | 99.107908 / 419.560 | 95.927228 / 495.392 | PASS / PASS |
| 4/fresh | 92.851405 / **1078.471** | 96.455885 / 453.378 | **FAIL RTT** / PASS |
| 32/fresh | 94.424269 / 482.074 | 98.015642 / 429.049 | PASS / PASS |
| SINGLE/retained | 82.662741 / 442.363 | 81.028710 / 375.014 | PASS / PASS |
| 4/retained | 81.605427 / 336.418 | 81.893786 / 425.391 | PASS / PASS |
| 32/retained | **68.327834** / 618.173 | **71.740075** / 513.179 | **FAIL throughput / FAIL throughput** |

The 4-stream RTT spike was probe 6 at scheduled 11s in round 1; it is retained,
not dropped as an outlier. No progress blackhole or descriptor-limit exit was
observed in these 12 rounds, which does not prove field Speedtest reliability.
Filtered per-second bytes, chronological probes, tails, delivery state and
physical occupancy are committed in
[`evidence/cleanup-qualification-20261018.json`](evidence/cleanup-qualification-20261018.json)
so the findings survive GitHub artifact expiry. Retained-state throughput and
RTT spikes remain blockers; the cleaner expansion is still experimental.

## Targeted local checks

PASS: journal/wallet/flow/datapath package tests; focused RC8/proof/cleanup
regressions; mailbox APPEND-hint/binary/delete/scope tests; focused race checks
for journal/datapath/tunnel; source identity checks and 92 Python tooling tests.
The recovery selection also passed the 61s long-blackout test preserving
in-flight TCP and messages. CI runs its independent vet/recovery and Android
checks; neither local source checking nor compilation is a device test.

## Remaining blockers and continuation

Actual 10 Mbps early startup remains red on this 2-core execution environment.
Both the 4 MiB candidate and an otherwise identical 2 MiB comparison binary
timed out on the first loaded RTT probe at its unchanged 1500 ms deadline.
The first measured interval terminated after about 2.5–2.8s; these are failures,
not completed 60s passes. The low-capacity profile was active before opening
application sockets. The comparison retains evidence that larger cleaner
reads did not introduce this already present failure mode; it does not prove
equivalence on every low-rate/provider path.

The failed 4 MiB startup snapshot had client logical flight 950,272 bytes
across 30 ranges despite an already contracted 65,536-byte admission window;
server flight was 786,432 bytes with a 425,984-byte window. Inspect the
per-empty-stream discovery exception in `deliveryController.plan` and
`bulkAdmissionAllowed`: its aggregate effect with many streams deserves a
targeted test. This snapshot is not proof that all excess was newly admitted
after contraction. Any correction must still allow recovery around a stalled
BODY without releasing old debt or stranding independent TCP streams.

Do not promote RC8 while any required gate is red or unrun. Preserve the
original >=75.000 Mbps each way for full 60s and last 30s/10s, max loaded RTT
<=750 ms, zero missed/timed-out probes, positive duplex every second and
original/new socket recovery. Low-capacity profiles retain continuity and
RTT requirements without claiming an impossible 75/75 Mbps at a 10 Mbps cap.

Next: investigate the retained 32-stream throughput misses and the fresh
4-stream 1078ms probe using the committed timing/occupancy evidence; address
early low-capacity backlog formation and genuine 10/20 Mbps startup/downgrade
cases. Retest RTT changes against these same throughput/tail gates. Real
Android/Rambler Speedtest validation and the jitter goal remain outstanding.
No RC8 release tag has been published by this work.
