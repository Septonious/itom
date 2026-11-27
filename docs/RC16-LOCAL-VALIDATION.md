# RC16 local validation checkpoint — 2026-11-27

Production source remains exactly commit 90b112990c3dffcf1fd274ab9127ae00ceedb306, src tree `6bb142d8286bf431a78bae1bd5f9b2d80e714b44`. No transport change is introduced by this validation record. RC7 baseline: `598a048b5addeace7ad7c08f3531688ebc8a70be`.

## Full local regression

Offline Go 1.27.1, race detector, GOMAXPROCS=2, build parallelism 1, test parallelism 1, fresh processes in batches of three, GOMEMLIMIT=1800MiB, GOGC=50. All 88 batches passed, 252 discovered top-level tests, 250 executed and passed, 2 explicit replay-dependent skips (TestRecordedFieldQualification and TestRecordedRC7Comparison). Mailstream 123/123, core 72/72, native 41/41, qualification 14/14 executed. No OOM kills. Reference RC20 routing assets passed. The original idle-writer EOF integration and video keepalive recovery passed in the complete sweep.

## Recorded native benchmark evidence

The four exact-source RC16 writer-fix replays completed near the previous session's end and were assessed during this session with both gates. All use the same provider and worker instrumentation as RC7, no relaxed acceptance thresholds. Mbps below are application goodput or cellular recovery-window goodput, as labeled.

| Normal link | RC7 direct | RC16 direct | RC7 encrypted | RC16 encrypted |
|---|---:|---:|---:|---:|
| Transport P95 (ms) | 78 | 96 | 142 | 97 |
| Download Mbps | 373.82 | 375.36 | 315.50 | 372.51 |
| Upload Mbps | 379.75 | 378.63 | 321.02 | 366.86 |
| New field gate | pass | pass | fail | pass |
| Original gate | pass | fail | fail | fail |

RC16 direct exceeds the RC7-relative P95 tolerance (78 -> 96 ms); original gate fails on 221.318 ms account maximum. Encrypted original gate fails on 205.338 ms upload application maximum. These are single recorded replays, not repeated field measurements.

## Rejected experiment

Cellular-only bounded second-account probe after six moderate-latency demand ticks; rollback on severe latency, missing evidence, no demand, or no improvement. New behavior test failed on original controller and passed with candidate, including all seven subcases and existing cellular policy tests under race detector. Actual direct replay failed. Baseline RC16 writer vs experimental: median negotiated RTT 544 -> 512 ms, P95 2321 -> 2452 ms, recovered download 2.552 -> 3.216 Mbps, upload 2.517 -> 2.359 Mbps, downloads 0/8 -> 0/8, no post-load echo. Last download first payload 2416 -> 1661 ms. Candidate excluded. No Wi-Fi/ encrypted replays of rejected change required for selection. No live provider operations.

Worker SHA256 (instrumented native local build): RC7 `633e3f7e6f5f5ad24cf64b3f401561587c024453c96e3bc1ad4c598cb0e5c0c9`; RC16 `a727a6959ac886e32e91ba6c702b3da536f5f2f999db3980cbbcc263b916bfa6`; rejected probe `30951b633397ac8dbb11381671abc9ed37c20a233db78dd315308d1b3bbc3d88`; common provider `c70e81118f4ed9b0cb3a8d4f09b692373e0d1d7e1252e0db7254023987eee03f`.

Full unit/race validation is clean; performance qualification is **not** complete. Retain RC7 baseline and archive. No release, APK build, or Actions triggered. Raw benchmark captures and logs are retained in the user's local evidence archive, not uploaded to the public repository.
