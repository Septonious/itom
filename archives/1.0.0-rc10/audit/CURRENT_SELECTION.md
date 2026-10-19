# RC10 selection status

Candidate source remains `05383c2` (source tree `ad90d1a900b7914ffce9bdc8482de1150fc21847`). It is exact RC2 transport policy plus the separately reviewed descriptor, cleanup-proof, observation, UI and identity fixes in `SELECTION.md`. No later transport experiment has been integrated or packaged.

## Retained for further comparison, not release-qualified

- Exact-sized journal scan allocations: same immutable transaction, bounded cursor and owned rows. Serial empty/sparse scan measurements improve time and allocation; full-page cost still needs measurement.
- Fixed 8 MiB hinted cleanup command bound: original proof/accounting and 32-UID bound. Some retained32 rounds pass unchanged throughput/tail/RTT gates; repeats still fail, so this is promising rather than certified.
- Skip speculative DATA cleanup only when no obsolete local ref or historical debt exists, with the required idle FIFO/ownership correction. No active cleaner is cancelled and no missing proof refunds debt.

Scripts preserve these independently on the pushed branch. They must pass the complete unchanged qualification together before becoming candidate source.

## Latest rejected bundle

Short recovery run `39207637183`: retained32 81.112/73.868 Mbps, minimum full/tail 72.893508 Mbps, max RTT 510.839 ms; matched control 79.980/73.641 Mbps, minimum 73.640619 Mbps, max RTT 390.085 ms. The earlier STATUS/sibling-route changes therefore show no repeatable improvement without a regression. Early10 failed cold probe 2 after one successful sample. Local repeat timed out probe 14, max successful RTT 1315 ms. Focused correctness passing does not override those failures. Do not apply this bundle.

Adaptive cleanup concurrency, measured-page policies, echo/publication-clock pressure bundles and tighter global pressure/escape guards remain rejected; their evidence is in the experiment JSONs and chronological handoff. Neither lower RTT with collapsed throughput nor an isolated favorable round is sufficient.

## Current hypothesis

Independent-stream discovery can exceed a small learned window by one full 32 KiB frame per empty stream. Test a bounded bulk-only record quantum which scales with that same window, rather than blocking unrelated streams or adding a second rate gate. Preserve classification explicitly so intentionally small bulk segments never consume interactive priority. Restore full records as the window grows. This is an experiment, not an accepted change.

All original quota, durable journal, unknown APPEND, wallet, recovery, Android identity and release thresholds remain mandatory. No RC10 APK or binary exists.
