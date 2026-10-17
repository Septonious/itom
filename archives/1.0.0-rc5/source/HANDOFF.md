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
