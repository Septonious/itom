# ITOM 0.9.2-Rev3 testing artifacts

This is the current development checkpoint, revision `transport-rework3-rev3`, based on the recovered original Rev3-testing source. Both endpoints must use this Rev3 build. Final sustained 100/100 Mbps qualification remains unmet.

Download the debug APK and the Linux binary matching your server architecture. Verify `SHA256SUMS`. Make the Linux executable runnable with `chmod +x` and replace it using your existing deployment procedure. Preserve server configuration, journals and wallet state.

**Android installation:** this APK has a new debug signing certificate and cannot update a differently signed Rev2 APK in place. Back up profiles before uninstalling the old app, then install this APK and restore/reconfigure profiles. Application ID: `org.itom.client.dev.rev7`; version code: 9203. The public certificate fingerprint is in `APK_SIGNER.json`; no private signing key is distributed.

The current changes retain the original Rev3 queue, fairness, recovery and feedback architecture, and refine publication accounting, peer-credit queries, completion indexing and per-stream scheduling. The executable source archive and original-comparison report are included.

Build verification passed: 808 Go test events across 19 packages, zero failures, 13 optional skips; all three mandatory outage-survival tests executed. IMAP fork checks and Go vet passed. Focused race checks passed 86 tests. Python tooling passed 94 tests. Android passed 71 domain tests, both native ABIs, release Kotlin compilation, lint (zero errors, one existing warning), APK identity and signature checks.

Six serial fresh-state laboratory cases used 15 accounts, 80 ms command delay, a simulated 600 Mbps provider, 10 s warmup and 60 s bidirectional measurement (last 30 s tail); MULTI used 24 streams per direction. All six completed with 60 overlapping RTT samples each, zero probe timeouts and zero missed probes. Every case failed the strict 100 Mbps in each direction requirement. These are laboratory results; Android-device and real-provider verification have not run.

| Case | Upload Mbps | Download Mbps | Maximum loaded RTT ms |
| --- | ---: | ---: | ---: |
| single-1 | 104.526 | 87.355 | 527 |
| single-2 | 98.571 | 91.003 | 563 |
| single-3 | 122.517 | 80.172 | 486 |
| multi-1 | 98.107 | 92.554 | 558 |
| multi-2 | 90.929 | 92.222 | 543 |
| multi-3 | 95.036 | 91.663 | 464 |

GitHub Actions reconstructs the already built files from hashed archive parts, checks every asset's size and SHA-256, uploads a draft and verifies remote digests before publishing. This workflow assembles the release files; compilation and the build checks above ran in the development workspace.

For field testing, pair SINGLE and MULTI runs on the same phone, network and Speedtest server; capture exact UTC start/end, mode, account count, both endpoint revisions and both diagnostic logs. Compare fresh-start and back-to-back runs, and check small application exchanges during load. Keep raw logs and credentials private.
