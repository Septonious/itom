# ITOM 0.9.2-Rev2 verification

Executed 2026-10-14 on Linux, Go 1.27.1, two available CPUs/GOMAXPROCS=2. Performance measurements ran serially, without concurrent compilation, race tests or Android builds. Runtime implementation commit: `4f9c0cb`; documentation/evidence follow without changing the tested Go/Android runtime.

## Outcome

APK and Linux amd64/arm64 builds passed the required correctness, source identity and build gates. The complete 100/100 Mbps performance qualification FAILED. This release is a candidate, with real Android/provider validation NOT RUN. It does not resolve the MULTI throughput limitation demonstrated below.

## Correctness and build checks

- Targeted Go tests followed every major runtime change: epoch/cold probe reset; atomic DATA lifecycle; guarded DATA IDLE/reader repair; independent fast/bulk service; atomic CONTROL lifecycle; ACK-time utilization; measurement boundary; cold placement; calibration and RTT diagnostics. Failure reproductions precede the relevant fix in the retained logs.
- Full normal core: 19 packages, 788 passing test events, zero failed events. Thirteen optional long-load tests skipped in that gate; the separate account and strict qualification runs executed the applicable loads. All three required IMAP outage/survival scenarios executed, and a required SKIP would fail the release gate.
- Bundled IMAP fork tests, reviewed fork-manifest pin, module verification and Go vet passed.
- Final focused race run: 60 passing test events, zero failures. The earlier durability/receive race run recorded 94 passing events before the final startup/diagnostic additions. The final run covers those additions and quota, scope, cancellation, reader/session and two-user behavior.
- Native host tests with `itomtest`, including the IMAP/TCP bridge, passed during the Android build. The tag is absent from release binding/binaries; its optional commit-delay API only adds latency and cannot disable actual fsync.
- Kotlin/domain: 71 tests passed, zero failures/errors/skips. Android release Kotlin compilation and debug APK assembly passed. Lint: zero errors, one existing `ModifierParameter` warning in `Theme.kt:111`.
- APK identity: `org.itom.client.dev.rev7`, code 9202, name `0.9.2-Rev2-debug`. Both native ABIs contain the expected Rev2 revision and their SHA-256 digests were checked.
- APK signing verified successfully with Signature Scheme v2. Public certificate SHA-256: `c9c766e99b9b691044bd7fd2a5613ceb15fb3266f87aee225a84f56f160a8e45`, matching the published Rev1 local debug signer. The user authorized a clean local reinstall if needed. No signing key is included.
- Core runtime SHA-256: `94c85c536178126c2887d7fb2bfe951fb2f3ed1353c71a5da6f2b22f25d50290`, identical before/after required tests and in the packaged source. Linux amd64 version command reports Rev2; arm64 cross-compilation succeeded.

## Strict laboratory repeats

Fifteen accounts, one stream per direction in SINGLE and 24 in MULTI; 60 measured seconds requested, 10-second warm-up, 80 ms declared command delay, 600 Mbps provider cap, fresh endpoint state per repeat. Useful payload patterns are verified; fixed byte clocks prevent late/drained bytes from increasing the result. Each case preserves source/log hashes. No tolerance replaces the 100 Mbps floor.

| Case | Upload/download means Mbps | Tail upload/download Mbps | Loaded RTT max ms | Outcome |
| --- | --- | --- | ---: | --- |
| SINGLE 1 | 125.252/105.653 | 125.733/105.356 | 489 | PASS; all 60 probes overlap, no missed/timeouts |
| SINGLE 2 | 131.968/102.691 | 121.312/108.833 | 448 | PASS; all 60 probes overlap, no missed/timeouts |
| SINGLE 3 | 118.209/103.040 | 99.021/108.196 | 423 | FAIL; upload tail below 100 Mbps |
| MULTI 1 | 46.904/14.152 before early stop | Incomplete | 992 before timeout | FAIL; probe 11 timed out at ~11 s |
| MULTI 2 | 28.927/23.059 before early stop | Incomplete | 973 before timeout | FAIL; probe 12 timed out at ~12 s |
| MULTI 3 | 24.475/25.146 before early stop | Incomplete | 970 before timeout | FAIL; probe 16 timed out at ~16 s |

A maximum before timeout does not exclude that timed-out probe. Truncated MULTI averages are not sixty-second sustained results. Two passing SINGLE repeats and whole-interval means above 100 do not qualify the failed repeat set.

## Account scaling and cold-start diagnostics

The initial matched 20-case matrix and final-source eight-case 5/8/10/15 matrix are preserved. Final regular field-derived wrapper stress, eight-stream MULTI, 20 seconds measured after separately recorded cold byte windows: all eight intervals completed without a missed/timed-out probe, but only fifteen-account SINGLE met the 100/100 point target. Fifteen-account SINGLE was 162.9/104.9 Mbps with maximum loaded RTT 421 ms. Ten/fifteen MULTI downloads were both about 49 Mbps; fifteen had lower upload and higher maximum RTT. See `REV2_FIELD_FINDINGS_AND_RESULTS.md` for every case and account recommendation.

The full-tail stress case failed on a loaded timeout. Wrapper timings are not deconvolved wire measurements; DATA FETCH was absent in Rev1 and is modeled using an explicit proxy. These cases are diagnostics, not calibrated provider replay or real-world qualification.

## Artifact and evidence verification

The executable source ZIP excludes Git worktree pointers, generated binaries/caches and private diagnostic uploads. Its per-file SHA-256 manifest and offline launcher checks are verified after packaging. `verification.json`, `SHA256SUMS`, public signer metadata and the evidence ZIP accompany the release. GitHub publication reassembles fixed-size archive parts, checks each part and the complete ZIP, verifies every release asset's remote digest and size, then publishes the prerelease. Raw production diagnostics and credentials are excluded.

APK install/device execution, real-provider throughput and phone Speedtest SINGLE/MULTI were NOT RUN. Existing functional outage/two-user tests do not constitute two-user throughput qualification or a prolonged device soak. No timed fuzz campaign was executed; the normal fuzz seed corpus ran with the correctness suite. The candidate needs matched phone/provider trials, including finite startup phases and account counts, to establish the requested field target.
