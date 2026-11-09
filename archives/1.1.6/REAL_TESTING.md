# ITOM 1.1.6 RC1 — real-device experimental testing

This release is **not stable and not field-qualified**. It uses a mandatory new ITOM4/ASCII85 IMAP mail envelope, deliberately incompatible with ITOM 1.1.5. The successful synthetic five-pair reference is [Actions #42650945537](https://github.com/Septonious/itom/actions/runs/42650945537).

## Files and compatibility

- **ITOM-1.1.6-RC1-TESTING.apk** — signed, installable *testing* Android application `org.itom.client.testing`, version `1.1.6-rc1-testing` (code 10160), Android minimum API 29. May coexist with stable `org.itom.client`; the data and profiles are isolated. Android debug signing is **not** compatible with stable. It is a debuggable build, not suitable for production or untrusted field distribution.
- **ITOM-1.1.6-RC1-UNSIGNED.apk** — main application identity `org.itom.client`, built release variant but **cannot be installed** without signing. If CI has the exact original signing key, the asset is instead **ITOM-1.1.6-RC1.apk**, and its signer must match SHA-256 `0fdd51e00489c93b13ed5ce35e13257a9c8964f2f93b1660e5e542cfbf6fb4f0`.
- **ITOM-1.1.6-RC1-linux-amd64.bin / linux-arm64.bin** — corresponding server management binaries, verified against their revision.
- **ITOM-1.1.6-RC1-sources.zip** — complete source. **ITOM-1.1.6-RC1-QUALIFICATION.md/JSON**, **ITOM-1.1.6-RC1-synthetic-rtt.zip** — synthetic measurements. Verify `RC_STATUS.json` and `SHA256SUMS` before installing.

## Safe manual test procedure

1. Back up the 1.1.5 server binary, service unit/configuration and `/var/lib/itom`, including profiles, ledger and wallet state. Do **not** delete IMAP folders or provider mail.
2. Install **ITOM-1.1.6-RC1-TESTING.apk** as a separate app. Set up a test profile (the stable app's internal profile data is not shared). You can keep the stable app installed for rollback; only one Android VPN should be active at a time.
3. Install the matching **1.1.6** server binary on the *test server* or during a controlled service outage after backing up production state. New 1.1.6 clients cannot communicate with the 1.1.5 server. Run the binary's `version` command and verify `itom-1-1-6-rc1` before replacing any existing executable.
4. Test upload and download, plus idle, loaded and post-load application RTT under 5/20 Mbps, typical cellular, and home networks. Verify DNS, session startup, TCP half-close, reconnect after changing radio network, sleep/resume, cleanup and battery impact. Record IMAP account ready/writer counts and diagnostic error logs.
5. Roll back **both** ends to the backed-up stable 1.1.5 configuration if there is a disconnect, data corruption, unreclaimed provider storage or severe delay. Restore retained state using the documented backup rather than clearing app data or purging mail.

The CI model's 5/20 Mbps case improved measured verified application throughput from 3.27→3.60 Mbps up and 13.07→14.36 Mbps down. This is **not** a guarantee on a real phone, real IMAP server, or changing radio network. The 3 ms ICMP-floor model had a 98 ms loaded p95 regression and needs particular attention.
