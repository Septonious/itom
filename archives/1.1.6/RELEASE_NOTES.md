# ITOM 1.1.6 RC1 — experimental low-latency candidate

**Not stable, not yet field-qualified.** The exact ITOM 1.1.5 stable release stays unchanged.

## Changes
- Removes the periodic two-second NOOP from the continuous control UID FETCH receiver. Empty FETCH latency is measured instead.
- Adds experimental latency-aware account scoring, but leaves it **disabled for this RC** after paired trials showed worse home/mobile loaded p95. Production retains 1.1.5 round-robin routing and the two-copy isolated-request hedge without added account concentration.
- Adds a **borrowable fourth APPEND completion slot** for interactive bursts. A small control message may use the fourth slot while bulk DATA temporarily uses at most three; bulk-only operation retains all four. No extra login, writer sockets or unbounded workers.
- Adapts loaded-feedback coalescing to observed control-FETCH RTT (12, 16 or 24 ms); idle feedback remains 2 ms with the existing 16-packet limit. Faster credit/receipt updates are balanced against provider APPEND overhead.
- Preserves bulk striping, bounded MIME batches, session/window controller, recovery fences, mailbox charge rules, four live connections/account, server-only cleanup and profile schema.
- Adds focused selection and empty-FETCH timing regression tests.
- Adds separate **3 ms home ICMP floor**, **15–30 ms home IMAP-path**, and **30–150 ms mobile IMAP-path** fixtures with measured TLS-echo RTT distributions, rate-constrained transfer checks, and five independent paired repetitions per scenario. These are synthetic models, not measured Rambler command RTTs.
- [Five-run RC1 qualification](https://github.com/Septonious/itom/actions/runs/42636826612) verified the exact test source at `c3c6fdbfb9a91433846f35f6975fbf3bbc62527e`. On the 15–30 ms home path, idle median **76.9 → 72.9 ms**, pooled idle p95 **105.1 → 97.8 ms**, pooled loaded p95 **362.2 → 354.7 ms**, with **307.3 Mbps upload / 330.1 Mbps download**. All five candidate home runs sustained more than 200 Mbps in each direction.
- On the mobile fixture, idle median improved **130.7 → 122.8 ms**, but pooled idle p95 regressed **235.6 → 247.3 ms**; this tradeoff remains explicitly unqualified for field promotion.

## Performance acceptance
- Primary objective: lowest stable idle/light-load application RTT, ideally median <=80ms and p95 <100ms on a suitable home link. Do not apply home RTT criteria to the mobile 30–150ms IMAP distribution.
- High-capacity reference physical transport must sustain >=200 Mbps **per direction** for final acceptance. 300+ Mbps is desirable but optional; mobile speed must be evaluated relative to the physical network.
- Keep recovery, cleanup proof, mailbox limits, multi-user fairness, Android CPU/wakeups and long-run application usability.
- GitHub Actions synthetic comparisons do **not** establish a real-device 200 Mbps or sub-100ms guarantee. A qualified candidate must still undergo live user-device IMAP, phone energy/wakeup, multi-user, mailbox pressure, recovery, and multi-hour soak tests. Read `QUALIFICATION_5RUN.md` for the precise distributions and any regressions.

## Installation and signing
- Source baseline: exact tag v1.1.5; new branch codex/itom-1.1.6-low-latency. Android application ID remains org.itom.client, versionCode 10160.
- APK is suitable for **in-place update only if it is signed with the existing 1.1.5 certificate** SHA256 0fdd51e00489c93b13ed5ce35e13257a9c8964f2f93b1660e5e542cfbf6fb4f0.
- If Actions has no retained private signer, the artifact is named UNSIGNED and cannot be installed. Never uninstall or clear app/profile/state merely to bypass signature checks.
- Install matching server and Android versions only after confirming APK signing and acceptable safety gates. Keep backups of server binary and /var/lib/itom; do not purge provider mail or journal/wallet state.
- This is a prerelease for measured field evaluation, not an automatic upgrade recommendation.
