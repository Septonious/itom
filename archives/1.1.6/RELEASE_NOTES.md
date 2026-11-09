# ITOM 1.1.6 RC1 — experimental low-latency candidate

**Not stable, not yet field-qualified.** The exact ITOM 1.1.5 stable release stays unchanged.

## Changes
- Removes the periodic two-second NOOP from the continuous control UID FETCH receiver. Empty FETCH latency is measured instead.
- Selects small interactive/control APPEND routes using fresh per-account IMAP command RTT, observed APPEND completion and actual priority queue pressure. Existing single-account fallback and the two-copy isolated-request hedge remain intact.
- Preserves bulk striping, bulk feedback batching, session/window controller, recovery fences, mailbox charge rules, four live connections/account, server-only cleanup and profile schema.
- Adds focused selection and empty-FETCH timing regression tests.
- Adds separate provider RTT profiles: HOME 15/20/30/18/27/22 ms and MOBILE 30/45/85/150/70/120/50/95 ms. These are explicit simulation schedules, not promises of application RTT.

## Performance acceptance
- Primary objective: lowest stable idle/light-load application RTT, ideally median <=80ms and p95 <100ms on a suitable home link. Do not apply home RTT criteria to the mobile 30–150ms IMAP distribution.
- High-capacity reference physical transport must sustain >=200 Mbps **per direction** for final acceptance. 300+ Mbps is desirable but optional; mobile speed must be evaluated relative to the physical network.
- Keep recovery, cleanup proof, mailbox limits, multi-user fairness, Android CPU/wakeups and long-run application usability.
- GitHub Actions synthetic comparisons do **not** establish a real-device 200 Mbps or sub-100ms guarantee. Live tests on the actual home/mobile paths are still required.

## Installation and signing
- Source baseline: exact tag v1.1.5; new branch codex/itom-1.1.6-low-latency. Android application ID remains org.itom.client, versionCode 10160.
- APK is suitable for **in-place update only if it is signed with the existing 1.1.5 certificate** SHA256 0fdd51e00489c93b13ed5ce35e13257a9c8964f2f93b1660e5e542cfbf6fb4f0.
- If Actions has no retained private signer, the artifact is named UNSIGNED and cannot be installed. Never uninstall or clear app/profile/state merely to bypass signature checks.
- Install matching server and Android versions only after confirming APK signing and acceptable safety gates. Keep backups of server binary and /var/lib/itom; do not purge provider mail or journal/wallet state.
- This is a prerelease for measured field evaluation, not an automatic upgrade recommendation.
