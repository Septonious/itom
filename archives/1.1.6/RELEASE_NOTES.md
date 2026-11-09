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
- **Mandatory ASCII85 MIME** replaces Base64 throughout every IMAP message (handshake, control, data and recovery), with no compatibility fallback. Format is ITOM4/7bit with 480-character lines. One output-buffer allocation avoids double-encoding costs. ITOM 1.1.5 peers cannot connect to this wire format.
- [Five-pair full qualification](https://github.com/Septonious/itom/actions/runs/42650945537) verifies the exact mandatory-ASCII85 transport at `52ce71e52846b3f188a0751f4b5c95a394cf3e78`, including a 20 Mbps down / 5 Mbps up mobile model. The candidate improved mobile-20 app throughput **3.27→3.60 Mbps up** and **13.07→14.36 Mbps down**, and pooled loaded p95 **1038→960 ms**. Home profile achieved **339.54 Mbps upload / 361.60 Mbps download**, with pooled loaded p95 **307→276 ms**.
- Known tradeoffs: the 3 ms home-floor model's pooled loaded p95 regressed 326.9→425.1 ms; mobile-20 loaded median increased 463.7→530.1 ms even as p95 improved. The experimental low-rate delivery-window contraction was reverted after it broke existing controller safety tests.

## Performance acceptance
- Primary objective: lowest stable idle/light-load application RTT, ideally median <=80ms and p95 <100ms on a suitable home link. Do not apply home RTT criteria to the mobile 30–150ms IMAP distribution.
- High-capacity reference physical transport must sustain >=200 Mbps **per direction** for final acceptance. 300+ Mbps is desirable but optional; mobile speed must be evaluated relative to the physical network.
- Keep recovery, cleanup proof, mailbox limits, multi-user fairness, Android CPU/wakeups and long-run application usability.
- GitHub Actions synthetic comparisons do **not** establish a real-device 200 Mbps or sub-100ms guarantee. A qualified candidate must still undergo live user-device IMAP, phone energy/wakeup, multi-user, mailbox pressure, recovery, and multi-hour soak tests. Read `QUALIFICATION_5RUN.md` for the precise distributions and any regressions.

## Installation and signing
- A separately signed **ITOM-1.1.6-RC1-TESTING.apk** uses application ID `org.itom.client.testing` and a test/debug certificate. It can be installed **alongside** 1.1.5, but is debuggable and is **only for field testing**. It has its own isolated app data and cannot import the existing app's private settings automatically. Never use this build as a production/stable release.
- The normal `org.itom.client` release APK is unsigned unless the matching 1.1.5 signing secrets are supplied. An unsigned APK is *not installable*. See `REAL_TESTING.md` for installation and server-backup instructions.
- Source baseline: exact tag v1.1.5; new branch codex/itom-1.1.6-low-latency. Android application ID remains org.itom.client, versionCode 10160.
- APK is suitable for **in-place update only if it is signed with the existing 1.1.5 certificate** SHA256 0fdd51e00489c93b13ed5ce35e13257a9c8964f2f93b1660e5e542cfbf6fb4f0.
- If Actions has no retained private signer, the artifact is named UNSIGNED and cannot be installed. Never uninstall or clear app/profile/state merely to bypass signature checks.
- Install matching server and Android versions only after confirming APK signing and acceptable safety gates. Keep backups of server binary and /var/lib/itom; do not purge provider mail or journal/wallet state.
- This is a prerelease for measured field evaluation, not an automatic upgrade recommendation.
