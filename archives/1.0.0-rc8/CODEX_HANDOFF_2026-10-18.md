# ITOM 1.0.0 RC8 — Codex development handoff (2026-10-18)

## Start here — current checkpoint, not a stable release

Repository: https://github.com/Septonious/itom
Branch: `codex/v1.0.0-rc8-development`
Last **tested source commit**: `91db2a504a0615c6c3837ccb84001322c73c5f6f`
Current working source root: `archives/1.0.0-rc8/source/`
Runtime revision: `itom-1-0-0-rc8`
Android application ID: **`org.itom.client`** (no dev suffix);
versionCode `10007`; debugging release name `1.0.0-rc8-debug`.
Build both endpoints from the same checked-out source commit.
Do not use `main` as the RC8 source: its top-level README reflects older releases.
The handoff/documentation-only commit made after the test commit contains
no transport or Android-source changes; the published CI artifact is still
built from the exact `91db2a5` source.

**Status: RC8 UNQUALIFIED TESTING CANDIDATE. DO NOT TAG/PROMOTE STABLE.**
Latest published GitHub prerelease is v1.0.0-rc6, kept unchanged.
The RC8 application was built and its test package created, but the
strict release acceptance and real-phone/provider testing are not complete.

## User priorities

1. Reduce RTT without sacrificing throughput. User screenshot shows two
   zero-TCP-stream Rambler accounts, both active lanes 2/2 and reported IMAP
   latency 7ms, yet Last RTT is **165ms versus 583ms**. This is account RTT,
   not proof of <200ms end-to-end app latency or a provider root cause.
2. Stable carrier recovery and **no failed Speedtests**. Never hide an
   unexpected carrier exit, RTT timeout, TCP reset or stalled download.
3. Deliver matched APK and Linux server binaries with evidence, while
   retaining app/package identity, server and client configuration,
   quotas, mail journals, accounts, and in-flight logical stream safety.

## Implemented and committed in RC8

Prior RC8 inherited the 16MiB server / 12MiB client logical flight, robust
physical APPEND / receipt accounting, 512-entry authenticated descriptor
cache (avoids false DESCRIPTOR_LIMIT at 129th valid physical report), lane
recovery, RTT-spike scoring for interactive carrier selection, and Android
high-contrast Material3 styling.

Commit `49836cb886457cca75113ca82a9c34ae9a864831` introduced a guarded
fresh-quiet-RTT placement update, a regression test and a matched testing
package workflow. An existing regression exposed a missed condition: no
active TCP stream does not imply no outstanding logical DATA after a
connection closes. Commit `91db2a504a0615c6c3837ccb84001322c73c5f6f`
fixes this by allowing fresh (<=10s) RTT to take precedence over an older
<=120s unloaded baseline **only** when no active logical TCP streams, no
outstanding logical bulk and no active physical/control work are present.
During loaded/stale/busy periods use established unloaded baseline and
fallback. Do not rewrite physical credit, APPEND journal, DATA ownership
or TCP identity from advisory RTT observations.

Tests include `TestRC8FreshQuietEchoOverridesOlderIdleBaseline` and
`TestLoadedEchoCannotRaiseIdleCarrierSelectionBaseline`. The corrected
focused Go/recovery selection CI passes.

## Exact final-source CI and build artifacts

**Build/package workflow: SUCCESS** (all 4 jobs)
https://github.com/Septonious/itom/actions/runs/39133679427

- Combined package, APK, Linux amd64 and arm64, executable source archive,
  signer evidence, and SHA256SUMS:
  https://github.com/Septonious/itom/actions/runs/39133679427/artifacts/12139251133
- Android-only artifact: /artifacts/12139550975 on the same run.
- Linux-only artifact: /artifacts/12138527892 on the same run.
- Source-only artifact: /artifacts/12139156532 on the same run.
- Artifacts are ephemeral (GitHub reported expiry around 2026-11-17).
- Debug APK installation compatibility depends on matching the **existing
  installed APK signing certificate**; retaining applicationId alone is
  insufficient. Confirm certificate before in-place installation.
- No RC8 GitHub release tag was created and no stable certification is made.

**Strict development CI: FAILURE (3 pass, 1 fail)**
https://github.com/Septonious/itom/actions/runs/39133679435

10 mail accounts, simulated 600Mbps provider, 60-second full-duplex,
80ms modeled command cost, 2-second RTT probe cadence.
All Mbps values below are laboratory values, not Rambler field claims.

| Case | 60s up/down | 30s down | 10s down | Max RTT | RTT probes | Gate |
| --- | --- | --- | --- | --- | --- | --- |
| 4 streams | 153.778 / 78.639 | 76.415 | 78.145 | 398.720ms | 30/30 | PASS |
| 32 streams | 132.029 / 78.237 | 76.065 | **73.374** | 401.053ms | 30/30 | **FAIL** |

Both runs retained the original TCP sockets and subsequent basic echo
functionality; the 32-stream 10-second download missed >=75.000Mbps.
Adjacent RTT jitter p95 from the same runs was **136.749ms** (4 streams)
and **148.720ms** (32 streams); a 100ms jitter objective is NOT qualified.
Focused Go source integrity, vet and account-recovery job PASS.
Android SDK37, tests, lint, dual JNI ABIs PASS.

**Earlier serious intermittent failure, not explained by newer passes:**
The immediately preceding source commit `49836cb` produced a 32-stream
download-progress blackhole after 51 seconds and one RTT timeout; its
4-stream tail was also under target. Preserve evidence:
https://github.com/Septonious/itom/actions/runs/39133443307 .
Do not claim that no Speedtests fail on the basis of the latest single
surviving 32-stream run.

**Historical unresolved RC8 failures:** genuine 10Mbps/20Mbps early
start/ramp creates delayed first RTT probes, >750ms RTT and/or timeouts.
Previous aggressive fast-grace, ACK-priority, and blind feedback-budget
experiments were tested and rejected because of throughput tails,
timeouts, and occasionally carrier exit. See:
`CI_CHECKPOINT_20261018.md` and `DESCRIPTOR_AND_RTT_AUDIT_20261018.md`.

## Required Codex continuation

- First diagnose the **intermittent 32-stream and SINGLE/4-stream download
  tail** from per-second delivered bytes, peer receipt/ACK age, queue depth,
  readable DATA grants, writer selection and client/server physical wallet
  occupancy. Compare both passed and failed *same-source* repeats.
- Investigate and fix sudden DATA download no-progress / unexpected carrier
  exits with finite, correlated physical-account and logical-socket traces.
  Never guess the failure root cause from 165ms account RTT alone.
- Reduce fast-path RTT via observed **proven idle** service and responsive
  lane selection, not by replacing overloaded RTT metrics with optimistic
  numbers. Do not globally reduce bulk flight/throughput.
- Run real **10 -> 20 -> 50 -> 150Mbps** start and **150 -> low** downgrade
  tests with application sockets created at the physically constrained stage.
  At low capacity, do not apply the impossible 75/75 Mbps throughput goal;
  require continuity, max loaded RTT <=750ms and zero missed probes.
- Repeat 60s full-capacity SINGLE, 4-stream, 32-stream, last 30s and 10s
  throughput, both fresh and retained mail/journal states, repeated 12-case
  matrix, race/recovery, provider pause/EOF, original/new TCP connections
  after load, and Android/Rambler real device field validation.
- Full-capacity gate is **>=75.000 Mbps in EACH direction for full 60s,
  last 30s and last 10s**, max loaded RTT <=750ms, zero missed/timed-out
  probes, positive duplex each second and original socket survival.
  Do not waive/round up. Keep jitter optimization tracked separately.
- Retain old server configuration, live mail credentials, client profiles,
  cryptographic identities, granted physical byte/message debt, journals
  and wallet; no database resets, speculative refunds or replay of an
  uncertain IMAP APPEND.

## Working commands

```bash
git clone https://github.com/Septonious/itom.git
cd itom
git checkout codex/v1.0.0-rc8-development
cd archives/1.0.0-rc8/source
python3 tools/build.py check --expect-revision itom-1-0-0-rc8
(cd core && go vet ./... && go test -p 2 -count=1 ./...)
python3 tools/build.py android --expect-revision itom-1-0-0-rc8 --accept-android-licenses
```

Primary workflow: `.github/workflows/itom-rc8-development.yml`
Secondary package builder: `.github/workflows/itom-rc8-testing-package.yml`
Single/low-link runner: `.github/workflows/itom-rc8-single-ramp.yml`

Start from the **last tested source commit**, not from a rejected RC8
experimental A/B. Modify only with independent before/after tests. Make a
new testing release only with matched APK and both Linux builds; do not
overwrite RC6 release artifacts or issue an RC8 stability claim while red.
