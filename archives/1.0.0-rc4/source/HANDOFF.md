# ITOM 1.0.0 RC4 — pressure-aware polling handoff

RC4 lives in archives/1.0.0-rc4/source on branch codex/v1.0.0-rc4. Runtime/CLI revision itom-1-0-0-rc4; APK version 1.0.0-rc4-debug, code 10003. The existing app ID and signer are retained for updates. Both Linux architectures are packaged. Update both endpoints, preserving all account identities, profiles, configuration, ownership and durable journals.

The RC3 field run improved control RTT to median 285.81 ms but regressed throughput and reported one failed Speedtest and physical reconnects. This fails the intended performance-preservation criterion. RC4 restores RC2 hot pacing for bulk and parallel setup, and restricts the twenty-millisecond cap to at most two quiet reserved readers with direct local hints and light demand. Shared empty-streak history is preserved. Read docs/rc4/INVESTIGATION.md for exact conditions, field observations and uncertainty.

The retained close history contains remote EOF on original sockets across accounts, plus one eight-second receive timeout. The trace window does not include the initial wave or failed Speedtest. Physical reader repairs and logical carrier reconnects are distinct: the end snapshot shows twenty connected lines, zero logical reconnect counts and repaired reader generations. Do not attribute remote closes solely to the timer change, and do not treat the lack of in-window failures as evidence that the reported run succeeded.

Targeted race and normal recovery checks are packaged with exact results. These verify policy boundaries, real supervisor/TLS/IMAP finite transfers, pause/blackhole/EOF recovery, original messaging and new post-load connections. They do not certify restored real-provider throughput or eliminate the need for field validation. Android/native/version/signing checks remain mandatory. The historical RC2 startup-discovery race timeout and remaining complete stable gates remain unresolved; all-internal/all-targeted/stable flags stay false. RC4 is a testing prerelease.

Build using the source tools and pinned toolchain, and consult VALIDATION.json and TEST_EVIDENCE.zip for exact scope. Actions verifies the hashes and identities of the locally compiled APK, Linux binaries, source archive and evidence before publication. It does not perform a live provider benchmark or claim an Actions compilation. The full project bundle includes source, binaries, APK, investigation, handoff, validation, signer and evidence. Private keys, raw user captures and local environment options are excluded.

For the next field pass prioritize sustained SINGLE/MULTI speed, no failed run, and original/fresh application traffic after load. Export paired diagnostics immediately after any reconnect or error so rotation retains the cause. Do not clear accounts or wallets to upgrade. Existing verified-TLS IP pinning, persistent connections, admin authorization and independent sixty-second rollback are preserved. Irreversible operations remain CLI-only.

## Earlier RC3 handoff (historical)

The following history describes RC3 and prior versions, not new RC4 test executions.

# ITOM 1.0.0 RC3 — latency candidate handoff

## Current candidate

RC3 is based on published RC2, on branch codex/v1.0.0-rc3, in archives/1.0.0-rc3/source. Runtime/CLI revision is itom-1-0-0-rc3. APK version 1.0.0-rc3-debug has code 10002 and retains application ID org.itom.client.dev.rev7 and the existing signer for updates. Update both endpoints while preserving account identities, profiles, configuration, ownership and durable journals. Linux AMD64 and ARM64 binaries are included.

The user now reports sustained high speeds with no failed Speedtest run. The current task prioritizes lower RTT while preserving that behavior. The paired RC2 captures show median control RTT 345.83 ms and minimum 216.21 ms. RC3 narrows a bounded response polling backoff from an 80 ms cap to 20 ms; this targets avoidable software waiting, not propagation delay. Ordinary active/quiet cadence, bulk batching, flight and physical accounting limits remain unchanged. Read docs/rc3/INVESTIGATION.md for exact measurements, mechanisms and limitations.

## Qualification boundaries

RC3 is a testing prerelease. Sub-200 ms and unchanged sustained real-provider throughput require new field measurements; neither is certified from a deterministic timing-policy test or a finite local transfer. The full internal matrix has not been run under the user's constrained usage budget. The historical RC2 instrumented startup discovery timeout remains unresolved, even though the new real-provider RC2 report has no failures. The release retains explicit false stable/all-internal/all-targeted gate flags and preserves the RC2 failure history as historical evidence, without presenting old results as RC3 test executions.

Focused correctness race logs, the before-fix backoff regression, normal supervisor recovery logs, Android tests/lint, native checks, release identity checks and artifact hashes are included in TEST_EVIDENCE.zip and VALIDATION.json. The normal finite MULTI test verifies twelve MiB in each direction across twelve streams through a temporary provider pause, all twenty original receive generations, the original messaging connection and a fresh follow-up transfer. This is a recovery/integrity check, not a sustained speed certificate. The final report is authoritative about actual pass/fail scope.

## Build, artifacts and upgrade

Build from this source root with tools/build.py and the pinned toolchain configuration. The release drivers record the local Linux/Android build, focused checks and packaging. Source checks validate the CLI, trace export and core/tunnel/version.go revision constants before compilation, and the final APK gate inspects actual native libraries and compiled APK identity. Version labels alone cannot substitute for native verification.

The APK keeps the same certificate and application ID as RC2. Preserve the deployed binary/configuration for rollback and follow the existing installation workflow. Do not clear wallets, journals or accounts to accelerate an upgrade. Hostname verification remains enabled when an IMAP destination IP is pinned; new server pins require a newly exported invitation or an equivalent phone profile change.

The full project archive includes APK, both Linux binaries, executable source with its manifest, investigation, handoff, status, validation, signer and test evidence. GitHub Actions reassembles hashed chunks, verifies the exact locally compiled source/runtime/APK/binaries/evidence, uploads a draft prerelease, verifies every remote size/digest and publishes it. Actions assembly is not an Actions compilation or a live provider benchmark. Signing keys, raw captures and local environment/JVM options are excluded.

## Follow-up

Compare chronological RTT and jitter before and after RC3 on the same route and Speedtest target, both unloaded and under the same SINGLE/MULTI traffic. Keep an original application stream open and verify fresh traffic after load. Export paired diagnostics immediately after any failure so rotation does not erase startup. The higher hot-window polling frequency may be provider-sensitive; field throughput and connection stability remain mandatory validation. The remaining stable gates and the historical intermittent startup-discovery condition need resolution before stable 1.0.0 promotion. Existing admin categories, single export progress indicator, server-authenticated admin role and independent sixty-second rollback remain intact; irreversible operations stay CLI-only.

## Earlier project history

The following RC2 handoff is preserved verbatim as historical context. Its tests and dates describe RC2, not new RC3 validation.

# ITOM 1.0.0 RC2 — project handoff

## Candidate and scope

RC2 is a targeted release candidate based on published RC1. It addresses an observed account-level IMAP stall amplifying application queue delay through stable feedback affinity. It is not stable 1.0.0. The full final internal suite and physical phone/provider qualification remain required before stable promotion. The user's strict sustained 100 Mbps target retains zero speed margin; no new live speed or jitter guarantee is made. The supplied RC1 field screenshots show substantial variation, including successful MULTI runs, but are not a new RC2 measurement.

Current source lives in archives/1.0.0-rc2/source on branch codex/v1.0.0-rc2. Runtime revision is itom-1-0-0-rc2. APK version name is 1.0.0-rc2-debug, version code 10001. Application ID org.itom.client.dev.rev7 and the existing APK signer are intentionally retained for updates. Both Linux AMD64 and ARM64 binaries are packaged. Published RC1 source/assets are preserved unchanged.

## Field analysis and production change

Read docs/rc2/INVESTIGATION.md for the bounded trace analysis and uncertainty. The ring buffers retain roughly the final ninety seconds; the initial failed MULTI tests are outside the available history. One account stalls across both endpoints with operations lasting up to sixteen seconds; its control echo reaches thirteen seconds while most other account operations stay short. The external backend/path cause is not proven. It is not evidence to reset every lane or clear durable account data.

The reproduced software issue is that stable bulk feedback affinity could continue selecting that delayed account even with responsive authenticated siblings. OPEN ranking also omitted elapsed in-flight control work. RC2 records the outer writer's start and applies an advisory cost only beyond a propagation-aware threshold bounded between one and three seconds. Bulk feedback no longer keeps its affinity in the primary set when its control writer has exceeded that budget. New OPEN, short DATA and bulk selection include the cost. Initial WINDOW affinity cannot bypass a detected stall. Quiet/completed writers have zero penalty and last-resort routing remains when no better account exists.

This does not move already queued account-bound frames, cancel healthy connections, replay uncertain APPENDs, or refund any physical journal debt. The existing cumulative logical protocol permits newer progress on an authenticated sibling to supersede an older observation. The correction creates no new wire format, account privilege or persistent configuration. All existing quota, cryptographic identity, receive cursor and cleanup proof rules remain authoritative.

RC1 early authenticated missing-range retention across physical writer ownership and BODY visibility grace is preserved. The timer alone grants no repair authority. The bounded 8–10 second receive no-progress floor and independent absolute command cap still distinguish temporary silence from persistent blackholes. Failed DATA receive snapshots continue retaining the first close cause after replacement. Persistent IMAP connections, paced role repair and hostname-verified IP pinning are unchanged.

## Validation and release procedure

The before-fix regressions, selected race checks, finite multi-stream pause/recovery checks and normal post-load checks are included with exact logs and hashes. Consult VALIDATION.json and TEST_EVIDENCE.zip for the final result, test names, Android domain/app counts, lint, signing and native identity verification. A finite integrity/recovery test is not a sustained throughput measurement. Historical RC1 capped-socket race timeout remains disclosed in its published archive; RC2 must record its own actual rerun result rather than importing a historical failure or silently declaring full qualification.

Build using the bundled pinned tooling from the source root. Release drivers record the local Linux/Android build, focused Go checks and packaging. GitHub Actions reassembles hashed artifact chunks, verifies source/runtime/APK/native identities and evidence, uploads a draft prerelease, checks every remote asset size/digest, then publishes it. Actions assembly is not an Actions compilation or live provider performance run. The full project bundle includes executable source, APK, both server binaries, investigation, handoff, validation, signer and evidence. Private captures, keys and environment options are excluded.

## Upgrade and operational boundaries

Update both endpoints. Preserve server configuration, account identities, client profiles, ownership and durable journals. Back up the deployed binary and configuration before using the established installation workflow. Do not clear accounts, wallets or journals as an upgrade/recovery shortcut. IP pins only change the TCP destination; certificate validation still uses the configured hostname. Export a fresh invitation or edit the phone profile when changing pins server-side.

The admin UI retains categorized controls, one diagnostics export progress indicator, server-authenticated admin authorization and an independent sixty-second rollback for reversible changes. Irreversible mailbox purges, uninstall and full account reset remain CLI-only. Global server-message changes clear individual overrides; individual messages otherwise override the global message and support Minecraft-style color codes.

## Remaining qualification

Repeat early MULTI startup and ramp on the working server, keep an original messaging connection open throughout, verify a fresh application connection after load and again after a quiet interval. If failure recurs, export paired client/server traces immediately to retain the failing startup. Compare chronologically adjacent unloaded and loaded latency samples rather than sorted RTT differences, and distinguish application cancellation from transport-originated resets. Stable promotion requires the complete internal gates plus sustained strict speed and real-device/provider follow-up. No confirmed physical Android fatal-stack diagnosis or blanket mobile-crash resolution is claimed by this RC.

## Explicit RC2 startup qualification limit

A repeated broader race run failed its pre-fault discovery exchange at the unchanged three-second deadline. The remaining twenty-four test events passed. One isolated traced race rerun passed, but its cause is unresolved. The focused new routing/early-NACK checks are separately recorded as passing; this narrower scope does not supersede the failed broader run. The package includes both the full failure and follow-up, and all-targeted/all-internal gate flags remain false. Keep RC2 classified as unstable until this intermittent startup condition and the remaining stable gates are resolved.
