## Updated experimental results — 2026-10-17 (NOT A RELEASE)

Current RC5 development branch contains two safe-to-test adaptive startup changes: (1) after the first complete, timely authenticated peer BODY receipt, the cold downstream BODY target can provisionally increase from 32 to 64 KiB; all later batch growth retains the two-receipt rule and two slow receipts restore 32 KiB. (2) while the logical bulk window is <=4 MiB, fresh useful ACK progress, actual outstanding flight >= half the current window, bulk age <75% of target, and no short-traffic pressure may double the next logical window probe. Physical IMAP polling cadence, account/scheduler worker caps (eight), journal debt and quota proofs, crypto, and wire format are unchanged. Unit and targeted race regressions passed using the supplied Go 1.27.1 binary.

The following are single, NON-QUALIFYING modeled-provider 60-second, 10-account, 24-stream-per-direction runs. Values are full-window useful application rates (download/upload Mbps) and maximum loaded echo RTT (ms); all had zero missed/timed-out probes and retained application connections, but NONE passed strict 100 Mbps in both directions:
- unchanged RC2 transport, GOMAXPROCS 2: down 51.485, up 120.905, max RTT 960 (one brief experimental process overlap makes this baseline imperfect).
- first BODY-receipt graduation only, GOMAXPROCS 2: down 58.310, up 126.139, max RTT 556.
- BODY graduation + ACK-driven small-window acceleration, GOMAXPROCS 2: down 57.191, up 130.438, max RTT 536.
- same combined candidate, GOMAXPROCS 4: down 71.539, up 148.295, max RTT 436.
- experimental *12* bulk workers with same candidate, GOMAXPROCS 4: down 69.704, up 150.545, max RTT 529. **Rejected and not committed**, since it reduced download goodput and increased RTT.

Short-test ramp observations: original ten-account download started at 7.864 Mbps in second one and did not sustain 100 Mbps. The combined candidate also began at 9.437 Mbps but momentarily reached 144.966 Mbps during second five before returning to a much lower steady rate. Neither peak nor any elapsed-time trim counts as sustained throughput. The performance gap is therefore not exclusively a startup issue.

`tools/rc5_gate.py` now defaults to **10 accounts**, runs three SINGLE and three MULTI full 60-second cases in both fresh and retained physical-mail state (12 cases), requires >=100 Mbps each direction across the full measurement AND sustained tails, max loaded RTT strictly below 750 ms, and requires 3-second mean useful ramp >=75 Mbps ending at second 5 and >=100 Mbps ending at second 10. Any missing test/telemetry, any speed miss, any probe loss or RTT breach fails closed. All tests MUST pass before any tag or release can be published. 125 Mbps sustained is the next target, not achieved. This is laboratory evidence only; actual phone/provider field qualification is still mandatory.

Next investigation: throughput per physical DATA account is approximately 5–8 Mbps in the current 10-account simulated provider scenario. Eight bulk workers are retained because higher fixed concurrency did not help. Profile APPEND/receive/journal service and CPU overhead rather than relaxing proofs or interpreting a burst as stable speed.

---

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
