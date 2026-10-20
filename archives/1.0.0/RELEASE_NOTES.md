# ITOM 1.0.0 — RC2 transport baseline

At the user's request, final1.0.0 restores the last field-usable RC2 transport (v1.0.0-rc2, a50a6a9e31ee9b38b53e84c9dce645e7ed7e415b). All238 non-test core files match RC2, except three release identity strings. Android runtime/UI are RC2; package identity remains org.itom.client, versionCode10014/versionName1.0.0. No later transport optimization, quota reallocation or journal policy is included.

This release includes APK, Linuxamd64/arm64, executable sources and the standalone POST_RC2_CHANGES.zip. Later work and per-file decisions remain under archives/post-rc2; the1.1.0 rework plan and protocol research are under docs/1.1.0. New idle/light RTT experiments are isolated and do not change this release.

## Validation scope

The build gates verify exact RC2 provenance, complete Go correctness/vet, quota/wallet/flow/datapath race suites, Android domain/app tests, lint, compiled APK identity/JNI revision, matched binaries and artifact hashes. A mailbox test's parent deadline is corrected to exceed its existing eight-second watchdog; production mailbox behavior is unchanged.

The original throughput/latency qualification scripts and thresholds are unchanged. User selection of RC2 as final is based on field experience. It does not certify every historical synthetic gate, repeated real-provider Speedtest reliability or universal sustained100Mbps/sub200ms latency. Known RC2 low-capacity startup/high-client-RTT limits remain documented in the archived paired evidence. No failed performance criterion is relabeled as passing.

## Preserve state during rollback

Preserve server configuration, account/user IDs, journals, mailboxes, the installed app and profile/wallet data. Older RC2 debugAPK used org.itom.client.dev.rev7; final keeps the current org.itom.client identity. The APK is a debug build, with its public signing certificate in SIGNER.txt. If persistent signing secrets are absent, a temporary debug certificate is used; it may not update an installed app. Do not uninstall or clear data merely to bypass a mismatch. Cross-version rollback of existing RC11–RC13 retained-state files is not certified by fresh-journal tests. Test on the separately planned server/isolated state without deleting old charged state or reusing incompatible live journals. Missing wallets cannot be repaired by resetting debt.
