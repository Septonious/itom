Testing release of the shared transport v2 carrier. Update **both** the Android app and Linux server together. Existing profiles and the saved Android signing key are retained.

Two DATA lanes per account, independent control traffic, a single server account bank, durable mailbox credit, bounded offline bootstrap, server-only audited cleanup, and accurate per-account diagnostics. Configured accounts are statically partitioned across enabled users (up to 5 users / 25 accounts; at least one account per user).

Verified locally: TLS IMAP/Noise/TCP repeated transfers and reconnects, five-user connectivity, native SOCKS/TUN HTTPS and DNS, SIGKILL recovery, variable 30–100 ms IMAP command delay and network replacement, race checks, 112 Android tests, lint, APK identity and signature. Real-provider RTT and throughput remain field-test targets.

Assets: arm64 APK and statically linked Linux amd64 binary only.
