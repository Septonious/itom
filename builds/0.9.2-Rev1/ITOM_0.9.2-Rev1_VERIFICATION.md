# ITOM 0.9.2-Rev1 implementation and verification report

Delivery date: 2026-10-14. Revision: `transport-rework3-rev1`. Status: implementation candidate; **100/100 Mbps field qualification is not achieved or claimed**.

The user requested wrapping up the current work. This package records the implemented rework and executed checks, with remaining performance qualification explicitly open. The attached Rev10 documents were treated as reference evidence, separately from the user's instructions.

## Implemented

- Removed the server's ten-account ceiling and Android's account-count ceiling. Transactional, serialized 1 MiB configuration/profile bounds remain. Identity-preserving save/import/export tests cover 1, 2, 5, 10, 11, 16, 32, 64, 65 and 128 accounts. Active sockets and login concurrency are independently bounded.
- Introduced authenticated protocol-10 handshakes and self-describing ITE2 DATA envelopes in both directions. DATA carries its original durable grant identity, canonical charge, ordinal, folder and encrypted body. Normal C2S reception no longer waits for a separately published usage descriptor. Authentication, AEAD and retained physical debt remain enforced.
- Pooled TCP stream state, credit and memory into one logical session owner across all physical accounts. Aggregate RX/TX budgets are 32 MiB; stream windows start at 1 MiB and grow on consumed data, bounded at 16 MiB. Logical sessions survive physical account repair. Per-account stream attribution avoids multiplying global stream totals.
- Added negotiated independent DATA readers/writers and DATA folders, with additive journal-scope migration. One shard per direction is the default; two/four static shards are optional. Available service is checked before advertising split capabilities. Low provider-session budgets use the two-folder fallback. The server reserves DATA-writing service before optional connections consume the account budget.
- Added aggregate/per-account socket admission, bounded parallel logins, deterministic initial account ordering and inactive-carrier rotation. Exported resource configuration is shared with Android. A large configured account set does not create unbounded active sockets.
- Fixed cross-folder UID/proof assumptions: recovery, inventory, deletion and durable cleanup now include actual folder and UID validity. Remote original-grant/ordinal lookup uses a rebuilt durable index. A folder is not a new quota allocation; an unknown write outcome is not refunded.
- Added bounded service-aware batching and account selection, maximum batching residence, small-DATA reservations and cumulative unsent cleanup-status coalescing. Bulk/fast delivery objectives remain 800/650 ms rather than expanding to accept measured congestion.
- Replaced the Speedtest gate entry point with a fail-closed laboratory runner. The named public-supervisor test must execute; fresh state, source fingerprints, useful TCP bytes, sustained/tail throughput and loaded application probes are required. Missing, skipped, empty, failed or stale-source evidence cannot qualify. Historical microbenchmarks remain diagnostic tests and do not imply field speed.
- Updated Go, native, Android and executable source-archive identity to 0.9.2-Rev1. Application ID remains `org.itom.client.dev.rev7`, version code 9201. No deployment or provider/device changes were performed.

## Architecture choice and limits

The candidate uses the hybrid of equal-role accounts, pooled logical streams, and independent control/DATA service within an account. A dedicated control-only account is not the default: it would concentrate control availability and make provider loss a cross-account failure. Pure two-folder mode is retained for low-capacity providers. Dynamic folder proliferation and automatic dedicated-account role reassignment were not implemented; the design comparison and experimental matrix remain in the full rework plan.

Provider session limits, command latency, visibility lag and real physical quota still bound throughput. More folders cannot defeat these limits. Account-scale coverage does not certify arbitrary-size live profiles. Two-user concurrent DATA integrity and repair passed; two-user throughput qualification remains open.

## Targeted Go checks after major changes

The counts below are Go test events (including subtests), not a count of independent real-world scenarios. Logs and hashes are included in the verification evidence. Tests that initially exposed defects were retained during development; the listed files are the passing regression runs after those fixes.

| Check log | Passed events | Failed | Skipped |
|---|---:|---:|---:|
| `01-account-limits.jsonl` | 31 | 0 | 0 |
| `02c-envelope-verification.jsonl` | 197 | 0 | 0 |
| `03b-runtime-envelope.jsonl` | 78 | 0 | 0 |
| `03d-envelope-race.jsonl` | 3 | 0 | 0 |
| `04-folder-migration.jsonl` | 64 | 0 | 0 |
| `05b-session-owner-credit.jsonl` | 70 | 0 | 4 |
| `06-socket-admission.jsonl` | 56 | 0 | 1 |
| `06b-resource-config.jsonl` | 20 | 0 | 0 |
| `08d-regression-proof.jsonl` | 53 | 0 | 0 |
| `09b-fixed-rtt.jsonl` | 27 | 0 | 0 |
| `11c-service-regressions.jsonl` | 55 | 0 | 3 |
| `12b-ordered-admission.jsonl` | 8 | 0 | 0 |
| `13-qualification-path.jsonl` | 8 | 0 | 0 |
| `16-native-full.jsonl` | 3 | 0 | 0 |
| `17-fixed-objective-regressions.jsonl` | 27 | 0 | 0 |
| `18-data-service-race.jsonl` | 10 | 0 | 0 |
| `21-public-services-race.jsonl` | 12 | 0 | 0 |

The latest race runs cover DATA-reader repair, quota/folder scoping, bounded work admission, low-session fallback, provider connection refusal and two simultaneous users carrying concurrent DATA. The native TCP/IMAP bridge suite passed with the test-only fixture enabled. The Android build passed 71 domain tests (zero skipped/failing), lint (zero errors, one existing ModifierParameter warning), release Kotlin compilation, debug assembly, APK identity and both ARM native-library checks. Android device tests were not run.

## Performance evidence: a failed target is retained

A 30-second, ten-account SINGLE-style run on real public Go supervisors over the local TLS IMAP provider fixture measured:

| Measure | Result |
|---|---:|
| Useful application upload | 103.049 Mbps |
| Useful application download | 78.731 Mbps |
| Loaded application RTT p50 / p95 / maximum | 312 / 433 / 449 ms |
| Loaded probes / overlapping probes | 30 / 30 |
| Missed probes / timeouts | 0 / 0 |
| Meets 100/100 Mbps target | **No** |

The fixture used 80 ms command delay, a 600 Mbps provider path and ten accounts. This diagnostic preceded the final DATA-reader SELECT-audit caching and reserved-writer fixes. Its log is `10b-single-snapshots.jsonl`. It is not the strict final 60-second qualification and cannot qualify the final source.

The final-source 60-second repeated SINGLE/MULTI qualification, calibrated/held-out provider traces, complete topology/provider matrix, long soak, actual Android Speedtests and real-provider measurements were **not run**. Therefore control/DATA correctness improvements and local race passes must not be read as proof that field RTT instability, SINGLE/MULTI inversion or the 100/100 Mbps target are solved. A full-suite race run and timed fuzz campaign were also not performed; normal Go tests execute the fuzz seed corpus.

Run `tools/transport_qualification.py` serially after building, with at least three fresh 60-second SINGLE and MULTI runs. Preserve all failures. Then run the planned provider/device matrix and soak; compare useful TCP bytes and loaded application RTT, not in-memory pump throughput or IMAP command latency alone.

## APK and upgrade

The user selected building locally on the machine holding the Rev10 key. The initial candidate delivery omitted the locally signed validation APK. At the user's subsequent request, this full build bundle includes that APK for testing; its local signer differs from Rev10 and it cannot replace that installation in place. See APK_SIGNER.json and rebuild/sign with the original key for an update. The public Rev10 signer digest and exact local-build/signing checks are in `REV1_BUILD_AND_UPGRADE.md`. Check signer continuity before an in-place update; do not uninstall or clear profile data to bypass a mismatch.

Upgrade both endpoints together, with a stopped paired-state backup. Preserve account identity, folders, wallet and journals. After Rev1 publishes physical DATA, restoring pre-upgrade quota state can forget exposure; an automatic downgrade is not safe. No exposure-preserving downgrade tool is included.

## Final build gate

**Passed:** the final pinned Go 1.27.1 server build gate executed 758 passing test events across 19 packages, with zero failures and 13 explicitly optional skips. All three required provider/account outage-survival tests executed and passed; none were skipped. The new performance qualification test was among the optional skips, and is not counted as performance acceptance. Vendored IMAP tests, module verification, dependency/fork pins and `go vet` passed. Linux amd64 and arm64 binaries were built; the amd64 version command returned `ITOM transport-rework3-rev1 linux amd64`.

The packaged source manifest was verified for all 685 files. The packaged first-party Go runtime hash exactly matches the final tested/built runtime:

```text
63c2bf56818f35078206dc5f6f51ec2961908c59359629204d306bd9cc6dda9c
```

Source ZIP SHA-256:

```text
9c437c8430a2cc996991b2db31026c7669bcef24fd61800a37d9a66bf86dc872
```

The executable ZIP's `check --expect-revision transport-rework3-rev1` operation passed on a fresh extraction. `verification.json` binds selected logs and final evidence to their SHA-256 hashes. `SHA256SUMS` covers the source archive, binary artifacts and delivery reports. Full-source performance qualification remains open despite the clean correctness/build gate.


## Subsequent GitHub build bundle

The user requested an APK, Linux binaries and full archive for GitHub. Android was rebuilt/reverified with the provisioned toolchain, and both Linux architectures were rebuilt. Their binary SHA-256 digests exactly match the earlier clean build-gate artifacts. The APK signature verifies under v2, but uses local debug signer `c9c766e99b9b691044bd7fd2a5613ceb15fb3266f87aee225a84f56f160a8e45`, which differs from the supplied Rev10 signer. No private signing key is included. Building/signing with the installed app's original key is still required to preserve its in-place update path.

Only documentation changed after the clean Go gate; the packaged runtime hash remains identical to that tested/built runtime. The source ZIP was regenerated to explain the requested debug APK. The new archive checksum is recorded above. This publishing step adds no field-performance qualification.
