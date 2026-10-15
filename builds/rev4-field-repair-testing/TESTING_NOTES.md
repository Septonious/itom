# ITOM Rev4 field repair testing 1 — UNSTABLE

This package contains the exact APK and Linux server executables that passed the final internal qualification. It fixes the DATA-reader stall through deleted UID ranges, independent SEARCH-index lag, old-range starvation and excess SEARCH commands during dense transfers. Android callback exceptions are contained during session cleanup.

All internal gates passed: 834 core test events in 19 packages, IMAP fork checks and vet; three repetitions of 14 application scenarios plus 14 with added durable-commit delay; accounting/recovery race checks; native TCP/IMAP bridge checks; 71 Android domain and four callback tests; Android lint (zero errors, one existing warning); and all six fresh SINGLE/MULTI runs. Every run passed the user-approved 10% tolerance: useful throughput >=90 Mbps including sustained tails, every loaded RTT <1,100 ms, zero missed/timed-out probes. These are laboratory measurements. See VALIDATION.json and FIELD_REPAIR.md.

The reported whole-app crash on mobile data remains unconfirmed: the supplied captures do not contain its fatal stack, and no physical phone or real provider was available. This is an UNSTABLE TESTING prerelease, not a stable release or a claim that the field crash is resolved.

## Install

Update both Android and the Linux server with this package. Choose linux-amd64.bin for an x86-64 server or linux-arm64.bin for an ARM64 server. Verify SHA256SUMS first, mark the Linux file executable with chmod +x, then use the same arguments/service configuration as your existing server. Preserve profiles, server configuration, journals, wallets and mailbox contents.

The APK is a debug build for ARM64 and ARMv7. It keeps application ID org.itom.client.dev.rev7, version code 9204 and the same certificate as published Rev3 testing / Rev4; install it over that app to preserve data. The app version still displays 0.9.2-Rev4-debug, and the binary revision still displays transport-rework3-rev4. The distinct testing filenames, SHA-256 values and STATUS.json identify this repaired build; no runtime/version change was made after qualification. If installation reports a signature mismatch, preserve/export your data before considering migration.

GitHub Actions reassembles the hashed archive parts, verifies every asset and its internal gate evidence, verifies remote asset sizes and digests, and publishes this uniquely tagged unstable prerelease. Compilation and qualification ran in the development workspace. The private signing key, user captures, account credentials and build caches are excluded. Earlier release assets are unchanged. The publication flags in VALIDATION.json record the earlier qualification time; STATUS.json identifies this testing package.
