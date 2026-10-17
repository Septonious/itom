# ITOM 1.0.0 RC5 — unpublished development checkpoint

## Baseline and source
RC5 starts from the **exact v1.0.0-rc2 source tree**, not the unstable RC3 or RC4 branches.
Rev/identity: `itom-1-0-0-rc5`, Android `1.0.0-rc5`, versionCode 10004;
unchanged debug package `org.itom.client.dev.rev7`. Update BOTH native endpoints together.
This is development source only, **not a published release and not certified for deployment**.

## RC2 vs RC3/RC4
- RC2: the user's real provider measurements indicate reliable ~50–90 Mbps, with some individual results beyond this range; official shipped artifacts still have an intermittent ten-account discovery/race timeout before fault injection. RC2's stall-aware routing avoids an account with a blocked control writer; keep this algorithm.
- RC3: decreased *all* hot empty-reader delays from 10,20,40,80,80 ms to 10,20,20,20,20 ms; field traces improved median echo latency but upload/download regressed and socket failures occurred. More frequent commands could produce provider pressure, but the remote EOF cause remains unproven.
- RC4: restored RC2 defaults and applied optional 20ms polling only to two quiet reserved readers, but field restoration of original RC2 throughput was never certified. Do not cherry-pick its polling policy without a controlled A/B.

## RC5 changes so far
1. Independent RC2-based source tree, Android and native version changes, source build/launcher identity validation.
2. `tools/rc5_gate.py`: unwaivable strict qualification. Three SINGLE and three MULTI 60-second runs each in fresh AND 6 MiB / 130 old-message retained state, 15 accounts, all upload/download full and tail >=100 Mbps. Every measured loaded RTT max must be <750 ms (explicitly parsed from individual source-fingerprinted logs). A missing/failed case or missing RTT metric fails. No averages or stability-only waiver. 125 Mbps sustained is the optimization target, not the current release floor.
3. No protocol/physical quota/cipher/UID cursor changes. Do not substitute RC4 test outcomes for RC5 evidence.

## Build environment
Provided user tarballs Go1.27.1 linux/amd64 and offline Go mod cache were safely inspected and unpacked in the analysis environment. Local private GitHub clone was unavailable (DNS blocked), so the complete source is not present there. Thus neither compilations nor simulated-provider speed qualifications have been run on this new RC5 tree yet. Nothing has been published. The uploaded Go binaries and caches are not added to GitHub.

## Strict qualification commands from source root
```bash
python3 tools/build.py server --expect-revision itom-1-0-0-rc5
python3 tools/rc5_gate.py --go /path/to/go1.27.1/bin/go --accounts 15 --repeats 3 --seconds 60 --minimum-mbps 100 --rtt-ms 750 --results reports/rc5
```
For the aspirational target repeat the gate with `--minimum-mbps 125`. These tests are **laboratory**, with modeled provider service, NOT equivalent to a real 125 Mbps phone test. Only move toward a release after the baseline RC2 startup race is investigated and RC5 passes every full correctness/race/Android gate plus field validation.

## Next code development
Reproduce the pre-fault ten-account startup discovery timeout under race instrumentation without changing its deadline. Record first reader/service availability, OPEN/CONTROL waiting, and original TCP identity at failure. Investigate single-account publication stalls while checking that the RC2 stable-affinity correction remains responsive. Optimize bulk scheduling/physical quota only after measuring proof-based capacity availability and actual logical ACK-progress. Preserve immutable unknown-APPEND charges, recovery proof rules, connection caps, and the original eight bulk worker bound. Do not apply aggressive global hot polling.

## Release gate
**No GitHub release/tag/publication** until *every* fresh and retained test passes >=100 Mbps each direction with <750ms RTT, without missed probes or after-load connection failures. RC2 remains the working physical-phone baseline; RC5 is not presently qualified.
