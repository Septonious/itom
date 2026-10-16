# ITOM 0.9.2 Rev8 handoff

## Status and deliverables

This is a testing prerelease, not a certified stable release. The user requested a fast, targeted verification pass and immediate APK, server binaries, complete source and GitHub upload. A full final-source test suite and physical phone/provider qualification have not been completed. Earlier Rev7 results remain historical evidence, not Rev8 certification. Consult STATUS.json, VALIDATION.json and TEST_EVIDENCE.zip for the exact successful and unsuccessful checks. A failed 100 Mbps or jitter measurement is not waived or relabeled as a performance pass.

Repository: https://github.com/Septonious/itom
Branch: `codex/rev8-stability-throughput`
Release tag: `v0.9.2-Rev8`
Editable project: `archives/rev8/source/`, or `itom/` in the executable source ZIP.

The full release ZIP contains the signed Android APK, Linux AMD64 and ARM64 executables, source archive, handoff, analysis, sanitized field correlation, validation evidence and checksums. GitHub Actions assembles the exact locally built bits from hashed archive parts, verifies their source fingerprint and native-library hashes, uploads them as a draft, checks every remote asset size and SHA-256, then publishes a testing prerelease. It does not claim to have rebuilt or field-tested those bits on GitHub.

## Changes from the supplied Rev8 source

The supplied branch was essentially the published Rev7 transport plus bounded diagnostic sampling of expected cumulative grant rollover. Keep that sampling: WALLET_EXHAUSTED before APPEND is an atomic lease rollover, not proof that an account or its journal is corrupt. Physical completion errors and other errors remain visible.

Rev8 now changes three latency policies. Once isolated short DATA/ACK samples establish the full-path baseline, the bulk governor permits 100 ms above that baseline instead of scaling extra delay with propagation RTT and retaining a 650 ms minimum. Uncalibrated paths retain the conservative bootstrap budget. Calibration still excludes repairs and traffic already sharing bulk load. This is a feedback threshold, not a guarantee that a provider can deliver every application exchange with under 100 ms jitter.

After two actual missing IDLE notifications followed by fetched rows, active-reader audit fallback is 50 ms instead of 150 ms, for the existing bounded 30-second interval. Quiet accounts retain their two-second cadence. A working notification path, an empty mailbox or merely having a TCP socket open cannot manufacture missing-notification evidence.

Control collection can now shorten its absolute flush deadline, never extend it when more urgent records arrive. Ready startup bursts and the existing work/record caps remain intact. The CLI revision and server capture metadata now report Rev8; the supplied source left both at Rev7. Executable source ZIP export now recognizes Rev8 APK/server filenames.

The strict laboratory qualification path now requires 100 useful Mbps in each direction, without the historical ten-percent speed margin. Full-window, final-30-second and final-10-second useful-byte measurements all contribute. All scheduled probes, original TCP/carrier identity, integrity and post-load recovery checks remain independent requirements. Adjacent RTT absolute differences are reported as a separate laboratory jitter metric; they are not claimed to reproduce Speedtest's estimator.

Increasing bulk workers from eight to twelve was evaluated and rejected because the measurement worsened. The final source keeps eight workers, the original bounded handoff queue, physical APPEND limits and durable accounting. No provider account reset, mailbox purge, forced physical-debt forgiveness or source-of-truth change is included.

## Field evidence and interpretation

The new paired October 16 captures identify a Rev7-debug client. They contain 118,531 client and 116,690 server trace events covering fifteen accounts. There are 210 complete paired control echoes: median RTT 358.39 ms, p95 691.92 ms, maximum 2,145.60 ms. Client enqueue-to-seal p95 is 53.31 ms, maximum 289.42 ms; server receive-to-reply-seal p95 is 111.27 ms, maximum 297.69 ms. Much of total RTT is outside these queue intervals. One-way differences are not treated as synchronized network measurements.

Application resets coexist with connected carriers. Generic client IO_ERROR and remote socket RESET records do not prove a unique Speedtest failure cause. A large cluster of local application resets is also consistent with the test cancelling its parallel sockets. Expected quota rollover is not a basis for throwing away charged debt. The precise Speedtest failure remains unconfirmed without a matching field pass of this candidate. Raw diagnostic ZIPs, target addresses, account identifiers, provider passwords, private signing material and profiles are excluded from publication.

## Rules to preserve

Count every physical APPEND attempt, including unknown outcomes, against durable byte/message limits. Do not replay an already sealed MIME body as a new uncharged attempt, reset counters, discard old debt or manufacture deletion proof. Cleanup remains authorized only by the existing receipt/deletion/fence rules. Cache entries and scheduler signals are not proof.

Keep physical socket caps, fast/control reserves, logical flight and shared handoff queues bounded. Slow journal/crypto/network work stays outside the ordered collector. Cancellation releases an unspent reservation while retaining published uncertainty. A carrier replacement must not conceal an unexpected healthy-load outage in a test. Preserve original TCP identities during physical repair.

Admin authorization remains server authoritative: an enabled client named exactly `admin` needs its own key challenge. Every reversible mutation arms an independent durable watchdog first. Sixty seconds without verified two-way recovery rolls back; sixty continuously healthy seconds commits. Permanent purges, uninstall, journal migration and full account reset stay CLI-only.


## Build and install

Use pinned Go 1.27.1 and Python 3.10+. Android tooling pins JDK 17, Gradle 9.3.1, SDK 37 and NDK 28.2. Standard full-gate commands from the editable root are:

```bash
python tools/build.py server --cache work/build-cache --expect-revision transport-rework3-rev8
python tools/build.py android --cache work/build-cache --expect-revision transport-rework3-rev8 --accept-android-licenses
```

The requested targeted finalization uses the archived release driver instead of asserting a full server gate. Its workspace/cache paths must be adapted on another machine. The precise performance command is:

```bash
python tools/transport_qualification.py --go /path/to/go --accounts 15 --streams multi --seconds 60 --repeats 1 --retained-state --minimum-mbps 100 --results work/rev8-load
```

Do not run builds or compression alongside this timed measurement. The qualification host has a two-core CPU quota. A timed-out test remains a failure; do not increase the provider bandwidth, remove retained state, remove samples, relax deadlines or substitute physical APPEND rate to claim the requested useful speed. SINGLE and additional repetitions were not run in the final targeted pass. Historical broader gates are preserved in older archives.

Update both the app and server; preserve the existing configuration, wallets, journals and state directory. Choose AMD64 on x86-64 Linux or ARM64 on ARM Linux. Restore executable permission if necessary and verify `version` reports `transport-rework3-rev8`. Ordinary upgrades require no account reset. The APK retains application ID `org.itom.client.dev.rev7`, version code 9208, and certificate SHA-256 `39667cd3f91521b09af012b2ca08f8f0ba0d7e07f14b32a9a1e794cb3f4bf999`, allowing an update over the published Rev7 APK. Keep the owner's signing configuration outside source control.

The CLI account database reset remains manager **3. Mail accounts → 6. Reset accounts database**, requiring `RESET ACCOUNTS`. It is not a mailbox purge or forgiveness of durable physical accounting. Global/individual formatted messages and authenticated reversible admin UI remain inherited; irreversible operations stay CLI-only. Admin mutations retain independent durable sixty-second rollback supervision. The removed settings introduction banner stays removed.

## Remaining qualification

Read VALIDATION.json before relying on the build. Passing targeted unit or recovery cases is not equivalent to a complete final suite. The older whole-app mobile crash has no verified fatal stack or confirmed root cause. A matching Android/server live-provider pass is still necessary: cold start, repeated SINGLE and MULTI, original Telegram/chat connection after load, a fresh connection, extended idle and Wi-Fi/mobile transition. Capture overlapping client/server evidence promptly after any failure, without publishing private profiles or raw captures.

If strict useful throughput or measured jitter fails, continue from this bounded candidate and its recorded failed evidence. Avoid speculative extra workers: the tested overlap increase was worse. Investigate per-stream prefix/NACK residence, control service, provider visibility and delivered-body occupancy before increasing concurrency. Keep all uncertainty and proof contracts intact. Until the outstanding failures and full qualification are resolved, this build remains unstable.

The final timed source fingerprint precedes two audited post-measurement corrections: the server diagnostic revision text and source ZIP export naming. VALIDATION.json and revision-metadata.patch record that exact delta. The publication verifier reconstructs the measured source fingerprint from the delivered sources to reject any additional change. Android/native host checks build from the corrected final source.
