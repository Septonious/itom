# ITOM 0.9.2 Rev10 — targeted recovery and admin UI testing build

A temporary failure of the first replacement IMAP control connection no longer immediately tears down its encrypted carrier. Recovery uses cancellable retries within the existing 15-second budget; minimum DATA capacity no longer adds an unconditional one-second initial sleep. Authentication/TLS errors still fail, shared LOGIN limits remain enforced, and uncertain APPENDs are never blindly replayed or refunded.

The Android admin overview now groups access/communication and system tools, with a section selector and separate Transport and Service categories. Diagnostics export shows one progress bar with a percentage.

Targeted testing reproduced original-code carrier cancellation, then verified 10 accounts and 20 lines recovered after all 50 client physical sockets were closed and each first control replacement returned EOF. Original application sockets and a fresh verified transfer survived. Minimum-service recovery was 1233 ms with race instrumentation and 1088 ms normally in the local test fixture. These are laboratory recovery times, not field guarantees. The retained post-load and capped-socket recovery checks passed. Android unit tests/lint, native host checks, vet and 45 source/build launcher checks passed.

The new field capture shows correlated client read EOFs while server IMAP connections remain healthy, but ends about two seconds later. Its root network/provider cause is unconfirmed. No bulk Speedtest transfer was run. This build strengthens recovery; it cannot prevent a remote peer or network from closing physical connections.

**Testing prerelease:** the full internal suite, live provider, physical phone, strict 100 Mbps throughput and jitter targets have not been requalified. Previous Rev9 performance results are historical, not Rev10 measurements. See VALIDATION.json and HANDOFF.md for exact scope.

Update both endpoints and preserve existing profiles, configuration and durable state. APK version code 9210 keeps the prior application ID and signer. AMD64/ARM64 Linux binaries, full sources, complete handoff and hashed evidence are included. GitHub Actions reassembles and verifies these exact locally built assets before publishing; it does not claim an Actions compilation or live performance test.
