# RC2 → RC10 source review and selection

Baseline: exact tagged RC2 tree `9cd9cc3fa2e07882b4d8154d7836e112840e3f1d`, checkpoint `ddd94e3`. No reset of accounts, journal, wallet, profile, logical identities, quota or physical proofs. RC2's field behavior is the baseline; its old intermittent discovery failure and lack of complete lab certification are not erased.

## What was reviewed

`release-source-inventory.json` inventories every changed path across RC2–RC9, including RC3/4 changes absent from current HEAD; it records per-release SHA256, RC10 SHA256 and a selection decision. `post-rc2-commit-history.tsv` includes intermediate and reverted experiments, including the non-ancestor RC3/4 tags. The two retained RC7 audits explain the older experiments and measured failures. RC8/9 chronological qualification remains in their original archives. Changes absent from the source snapshots (experimental pacing, journal sizes, session partitioning and controller clamps) were rejected in the earlier experiment ledger and are not reconstructed from favorable isolated results.

## Accepted and independently preserved

- Reclaimed descriptors: check the durable per-ordinal live-attempt index before caching replayed UsageReports. Cumulative Registered/Released counters cannot substitute for that proof. A replay still gets its cleanup acknowledgement; no debt is refunded or new attempt created.
- Descriptor headroom: finite 512-entry descriptor cache accommodates two 192-message physical inventories. Valid descriptors cannot be evicted without proof. This changes cache capacity, not journal quota or permitted physical inventory.
- Unhinted DATA cleanup: reclaim already eligible bodies after the first useful page instead of scanning another full page first. Original observation, deletion/absence and exact refund rules remain. A failed delete refunds nothing; cursor resumes later.
- Observation-only queue freshness, physical-to-ACK duration, descriptor occupancy and callback counters. No ACK age, accounting or admission decision reads these new diagnostic fields.
- Current `org.itom.client` app identity, no suffix, RC10 versionCode10009, matching native/server revision; compiled metadata checks and corrected binary naming. Existing signing mechanisms unchanged.
- RC8 dark theme, categorized admin Sections menu and one continuous diagnostic progress indicator. All existing admin rollback and irreversible-operation restrictions stay.
- Test fixture fixes: genuine low-capacity startup before application OPEN, distinct downgrade scenario, physical provider-wide session accounting, correct IDLE join floor, strict throughput/tail/RTT/probe/socket gates.

`preserved-patches/rc10-selected.patch` records the actual selected hunks against RC2. Other patch files preserve their RC9 context for review and must not be applied wholesale: mixed controller/cleanup patches contain explicitly rejected hunks. Positive changes are saved on the pushed branch rather than relying on an unpushed local stash.

## Rejected or deferred from this candidate

| Change | Decision and evidence |
| --- | --- |
| RC3 20ms pool-wide hot polling; RC4 conditional 20ms quiet-reader polling | Reject: user-observed speed/connection regression. Restore exact RC2 10/20/40/80ms hot backoff and original IDLE/audit scheduling. |
| RC5 doubled growth below 4MiB, first-receipt32→64KiB, warm128KiB resume | Defer: coupled faster ramp lacks matched repeatable stability proof. Restore RC2 half-window growth, two fast receipts and32KiB cold/resume. |
| RC6 max8MiB; RC7 max16/12MiB directional logical flight | Reject from baseline: retained throughput regressions. Restore RC2 symmetric16MiB, without changing physical quota. RC7's partial RC2 rollback retained the reduced ceiling and was not an exact RC2 control. |
| RC5 journal16/24MiB, physical pacer180Mbps, ACK clamps/urgent fairness, 4+6 sessions | Reject: documented upload-tail/starvation/RTT failures. Preserve existing12MiB journal policy and5+5 session partition. |
| RC8 fresh quiet RTT / loaded-spike placement score | Defer: useful unit invariants but no isolated repeatable field/performance benefit. Preserve original unloaded baseline and RC2 stalled-account routing. |
| RC8 cleanup2→4MiB; RC9 four parallel proof reads | Reject from initial selection: no independently repeatable tail benefit; restore original2MiB command bound and scheduling. Keep first-useful-page reclaim separately. |
| RC9 short-reader150ms audit | Reject: retained32-stream tails34/47Mbps and timeout; already reverted upstream. |
| RC9 always256KiB seed, discovery250ms pacing, ACK-floor calibration bundle | Defer: initial12/12 pass followed by8/12 repeat with51Mbps tail,1166ms RTT and stalled1s download. Keep RC2 seed, calibration and independent-stream escape. Calibration-only fix remains a future isolated candidate. |
| RC9 global two-frame escape and1s emergency | Reject: retained4-stream tail67Mbps and zero-progress interval; keep RC2 independent-stream progress. |
| RC5/6 throughput waivers and favorable-run-only certification | Reject: no qualification threshold relaxed. Historical failures remain failures. |

## Validation contract

Red/green tests reproduce both descriptor replay disconnect and over-scanning on exact RC2, then pass after the selected fixes. Fresh and retained SINGLE/4/32 must pass two60s rounds: >=75.000Mbps both directions over full/trailing30s/trailing10s windows, max loaded RTT<=750ms, every scheduled probe, positive useful duplex in every1s interval, original sockets and post-load integrity. True10/20Mbps startup cases keep their strict latency/continuity gates (a deliberately10Mbps cap is never claimed as75Mbps throughput). Quota/journal/proof/recovery race checks and Android SDK/lint/JNI/identity checks remain prerequisites to packaging. Passing a deterministic regression does not certify Rambler or sub200ms field RTT.

## Subsequent isolated measurements (candidate source unchanged)

Two matched retained SINGLE/32 A/B runs compared reference,4MiB hinted cleanup, and RC8 selection scoring. The larger read improved measured download roughly6–8Mbps in both repeats without a higher measured maximum RTT; only one SINGLE tail passed75Mbps. Retained32 remained71.976–73.885Mbps, so this is a promising isolated tuning change, not qualification. The selection change gave no consistent latency benefit and reduced retained32 download to64.557–65.571Mbps, with SINGLE max789.049ms once: reject from RC10.

RC7's16/12 directional bound was then isolated from its accelerated growth: retained SINGLE passed79.836Mbps minimum full/tail and427.404ms max; retained32 still72.693Mbps and510.853ms max. No convincing incremental benefit over cleanup4 alone; keep symmetric RC2 flight in the candidate.

A64KiB capacity-unmeasured cold probe plus the RC9 first-stream pacing/calibration hunks preserved existing recovery tests but still timed out under10/20Mbps. Logs exposed continued above-window escapes by established streams. A deterministic red/green regression reproduces that bypass and checks that a shared scheduling wake is released by an authenticated useful ACK or the existing bounded actor timer, with every other byte still charged. Extending pacing to those escapes passed the20Mbps startup case (30/30 probes, no timeout/miss,745.548ms max, original/new sockets intact);10Mbps still timed out. It remains an experiment, not a selected candidate policy. Both outcomes are retained in chronological evidence; neither authorizes a build.

Next isolated test bounds simultaneous local DATA proof-BODY reads to two. It does not limit cleaner ownership, remote/control cleanup, deletion or absence checks, and cancellation returns the scheduling slot. The shared12MiB journal, wallet, logical identities and all physical proof requirements remain unchanged. No experimental code has been applied to the RC10 source at this checkpoint.


## Later bounded experiments and current decision

All results are saved in `evidence/experiment-*.json`, including failed rounds. None changes the qualification criteria or authorizes packaging.

- Global above-window escape pacing with a 2s fallback violated `TestRev4DelayedBodiesAndReaderRestartsKeepEveryTCPStream`'s recovery deadline; reject. The 10Mbps result also exceeded 1s. Original independent-stream progress stays.
- Aligned quiet reserves passed SINGLE but retained32 download remained72.688Mbps. No demonstrated gain; defer.
- Bounded cleanup observation prefetch (eligible bodies first; only current-session committed local DATA) passed retained32 once at82.798/77.717Mbps, minimum full/tail76.607Mbps,max413.753ms. Unacknowledged bodies remain charged and present until their original ACK/delete proofs. A later repeat missed at74.029Mbps. Promising, not yet selected.
- Draining two large cached rows before another discovery was tested and rejected: combined retained download66.694Mbps. Restore the original four-row cache coalescing in the experiment.
- Measured C2S APPEND batch probes require two actual sufficiently sized successes, ignore fast/control/unknown outcomes, and reject repeated slow larger probes. A simple byte/time estimator permanently capped demand-limited healthy links and was rejected. One measured-size combined run passed retained32 at84.174/77.822Mbps,max487.550ms, but10Mbps startup still failed.
- Cold FIFO preparation cap4 with nominal server warm evidence was insufficient. Requiring two actual full BODY receipts avoided inventing measured parallelism from small receipts, but did not pass both blockers. New runner-only test uses cap2, proof within a propagation-aware250ms budget, and an isolated RC9 calibration correction. No logical flight/growth/polling/repair policy changes.
- Calibration defect: a range originally admitted at low flight can later experience a fully loaded epoch but still inflate the unloaded ACK floor. The isolated correction tracks peak exposure and uses half of the actual cold seed; existing windows, independent escapes and authenticated credit stay unchanged. Deterministic regression passes in the integrated scratch tree.

The candidate source is still commit05383c2; current experiments remain separate until they pass targeted checks. No RC10 APK/binary has been built.


## Final checkpoint for this work interval

Run39169810153: retained32 down72.487Mbps, max471.343ms; early10 timed out. Run39169810144: prefetch-only down70.215Mbps,max465.821ms. Neither qualifies. Their per-run evidence is retained.

The pressure-conditional escape experiment (64KiB cold seed,250ms wake, proof of exact wake ownership, peak/congestion calibration,cleanup4) preserved normal established-stream independence in deterministic tests. Its650ms default fast guard completed all30low-capacity probes and post-load original/fresh integrity, but max1444.943ms fails750ms. A350ms guard brought median334ms,max810.549ms, but upload fell21.692→8.454Mbps across the same10/20/50/150 profile; reject the tighter guard. These are synthetic local results, not a live Rambler claim. No threshold was relaxed.

The calibration test now derives cardinality/frontier from the actual cold seed, verifying both whole/split full-window exposure and exact authenticated credit without assuming256KiB. Broad focused controller/mobile/RC2/RC10 regressions passed0.085s for the isolated350variant; that functional pass does not certify performance. A prematurely started measurement was terminated before use and replaced with the complete recorded100.67s run. No favorable samples from that aborted attempt were used.

All source policies remain the reviewed candidate at05383c2. Unqualified experiments are stashed durably as separate scripts and evidence on the pushed branch. Retired prefetch/combined matrices are manual-only now; pushing an audit correction does not repeat known-red load tests. The strict main qualification and its package prerequisites are untouched. RC10 is not ready for APK/binary packaging.
