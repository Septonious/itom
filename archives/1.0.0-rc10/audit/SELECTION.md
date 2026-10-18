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
