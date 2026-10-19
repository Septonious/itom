# RC10 selection status

User explicitly requested a field-test package after reviewing the failures. Testing workflow builds after safety and Android gates; strict release qualification stays unchanged. The packaged core is exactly checkpoint522d7cf from the paired comparison. A later feedback-allocation experiment is archived, not selected; no controller policy changes. Packaging is authorized for testing, not certified performance.

Latest paired comparison39239603639: both original RC2 and current RC10 pass all six normal load cases (12 rounds each), while both fail10/20Mbps client ramps and100ms-client upload/tail. Performance differences are mixed, not universal improvement. See `RC2_HEALTHY_COMPARISON.md` and `paired-rc2-rc10-39239603639.json`. The corrected common core rerun39240291399 passes on both versions. No production change is selected from this comparison; packaging stays blocked.

## Current primary model: healthy server (supersedes old benchmark assumptions)

User reports an eight-core server with a 20 ms Rambler route and strong uplinks, and a phone route normally 20–50 ms, almost always <=100 ms. Primary qualification now applies independent below-TLS endpoint paths: server 20 ms / 1000 Mbps; phone 40 ms / 600 Mbps reference, with 20/50/100 ms client sensitivity and client-only capacity transitions. Backend service 2 ms and server capacity are configurable assumptions, not measurements. Actual CPU quota is recorded; local runs have two cores and do not certify eight-core hardware.

This checkpoint changes fixtures, documentation and qualification orchestration only. Production controller/framing, original state/proof/accounting rules and all numerical acceptance gates remain unchanged. Historical all-account provider blackouts and replica delay remain explicitly available in the manual legacy-stress workflow. Required normal checks retain client interruption recovery against an independently reachable server.

Focused endpoint isolation/capacity units, TLS NOOP calibration, client interruption/original-TCP recovery and legacy relay calibration passed. Offline source check and 91 Python tests passed. These are not full performance qualification. No RC10 package is authorized yet. Latest old-model failures remain historical evidence, not new-profile results. Useful-only cleanup, including the newly preserved fairness correction, remains NOT selected: broad recovery still failed.

Reference details: `source/docs/rc10/HEALTHY_SERVER_REFERENCE.md`. Required full healthy-profile qualification is pending. No claim that the user's pure TCP RTT has been independently measured: historical FETCH median ~15 ms / p95 ~45 ms supports a fast route but includes command processing.

Candidate source now adds the measured exact scan preallocation to checkpoint `05383c2`. Transport window, seed, routing, batching, polling and recovery policy remain RC2. The only new source hunk is the owned scan allocation; reviewed accounting/proof writes are unchanged. No controller or framing experiment has been integrated or packaged.

## Retained for further comparison, not release-qualified

- Integrated exact-sized journal scan allocations: same immutable transaction, bounded cursor and owned rows. Serial empty/sparse scans reduce allocation; full-page attempt/grant results and interleaved noisy grant repeats are retained. No blanket CPU-speed claim is made.
- Fixed 8 MiB hinted cleanup command bound: original proof/accounting and 32-UID bound. Some retained32 rounds pass unchanged throughput/tail/RTT gates; repeats still fail, so this is promising rather than certified.
- Skip speculative DATA cleanup only when no obsolete local ref or historical debt exists, with the required idle FIFO/ownership correction. No active cleaner is cancelled and no missing proof refunds debt.

Scripts preserve these independently on the pushed branch. They must pass the complete unchanged qualification together before becoming candidate source.

## Latest rejected bundle

Short recovery run `39207637183`: retained32 81.112/73.868 Mbps, minimum full/tail 72.893508 Mbps, max RTT 510.839 ms; matched control 79.980/73.641 Mbps, minimum 73.640619 Mbps, max RTT 390.085 ms. The earlier STATUS/sibling-route changes therefore show no repeatable improvement without a regression. Early10 failed cold probe 2 after one successful sample. Local repeat timed out probe 14, max successful RTT 1315 ms. Focused correctness passing does not override those failures. Do not apply this bundle.

Adaptive cleanup concurrency, measured-page policies, echo/publication-clock pressure bundles and tighter global pressure/escape guards remain rejected; their evidence is in the experiment JSONs and chronological handoff. Neither lower RTT with collapsed throughput nor an isolated favorable round is sufficient.

## Current hypothesis

Independent-stream discovery can exceed a small learned window by one full 32 KiB frame per empty stream. Test a bounded bulk-only record quantum which scales with that same window, rather than blocking unrelated streams or adding a second rate gate. Preserve classification explicitly so intentionally small bulk segments never consume interactive priority. Restore full records as the window grows. This is an experiment, not an accepted change.

All original quota, durable journal, unknown APPEND, wallet, recovery, Android identity and release thresholds remain mandatory. No RC10 APK or binary exists.

The first all-record quantum variant regressed delayed-BODY recovery (20-second socket read deadline exceeded). Reject it. A corrected fixture explicitly attaches the unit lane to its group; it previously exercised the no-stripe fallback. The narrowed variant limits only a stream with no actor-pending bytes, keeps subsequent records intact and explicitly preserves bulk classification. Focused datapath tests pass 0.653s; delayed BODY + reader restart and independent-prefix checks pass 26.894s. Load comparison is still pending; this correctness pass is not acceptance.


Prefix variant rejected too: GitHub39208684384 failed long-blackout recovery before retained load; early10 cold probe2 timed out. Local low profile completed all30probes and socket checks with no carrier resets but max1491.209ms and8.993/21.453Mbps, still a failure. Do not promote either framing variant.

Exact journal scan full-page check (two serial repeats, no local load/compiler overlap):256rows baseline4.252/3.707ms vs exact3.211/3.620ms, same~486KiB allocation; empty18.938–22.487us vs1.079–1.083us,49,568→416bytes;8rows153.096–155.999us vs113.444–130.531us,63,231→15,844–15,845bytes.32rows baseline535.531–635.262us vs392.687–510.346us,104,191–104,202→61,545–61,553bytes. Retain the exact-allocation patch. This does not replace end-to-end qualification.

Next isolated calibration test keeps all original RC2 framing, seed, growth, recovery and independent-stream discovery. Track peak outstanding logical flight during each original range's ACK residence, and exclude congestion/recovery/outage samples from unloaded-floor training. This corrects classification of observations, not ACK or quota authority. No shorter RTT target or extra retransmission is introduced.

Counter-hint allocation alternative is NOT selected: focused semantic tests pass, but it adds metadata lookups and allocations and has no established advantage over exact same-snapshot counting. Counter contents never controlled enumeration even in that experiment. Keep the simpler exact allocation variant. All journal tests pass on actual selected source (0.110s). Full main qualification is required after this source checkpoint; no build exemption.

Selected source tree: `8deb4a90d94b535e1383be8f4a01bf4533d4f21c`.

Honest-floor run39209203584 deferred: retained32 80.662/74.475Mbps, minimum73.828489, max438.222ms; matched control79.561/75.170Mbps, minimum74.545015, max427.247ms. Early10 timed out measured probe2 (first1277ms). Correctness tests passed15.527s, but the isolated controller still did not demonstrate an improvement without regression.

Full source qualification39209952896 on4c56511: core integrity/proof/recovery/race PASS. Fresh SINGLE both rounds PASS; fresh32 round1 PASS, round2FAIL74.221705Mbps. Other throughput/tail cases fail (retained32 minimum65.640858–68.769109Mbps). All12 full-capacity rounds preserve30/30probes, zero misses/timeouts and RTT<=556.227ms, but that does not waive failed throughput. True10/20 startup still times out. Separate startup SINGLE passes154.866/76.668Mbps,min76.414976,max541.316ms. APK/Linux/source-package jobs correctly SKIPPED; no artifact/tag.

Account command-admission diagnostic: urgent324 operations,max2.576ms, zero>=10ms; receive341,max1.287ms; maintenance105,max2.286ms; DATA78,max0.003ms; DATA receive327,max95.037ms(one>=10ms). Application still times out first measured probe1500ms. Do not change cap7 or reserve command capacity based on an unsupported hypothesis. Observer code stays runner-only. Initial diagnostic10s configuration was rejected by the existing60s guard before running; the recorded observation used60s, guard unchanged.

Next experiment isolates useful-only DATA cleanup with its FIFO/ownership correction from larger cleanup FETCH bounds. Selected source already has exact allocation. Original2MiB proof command cap, original pages, flight/seed/routing remain unchanged. Compare original bounds with/without no-work suppression, plus true10Mbps startup. Historical local debt and any eligible obsolete ref always keep the proof path; unknown/unacknowledged live DATA remains charged. No release acceptance before measured no-regression gains.
