# ITOM 0.9.2-Rev4

Rev3 could reconnect its mail carriers while application TCP remained unusable. Rev4 fixes premature DATA reception during bootstrap, lost OPEN answers on canceled physical actors, selected-carrier OPEN routing, serial mailbox overhead, and outage recovery feedback/retry pacing. Logical ownership, byte integrity and proof-based quota accounting remain enforced. The new application suite covers concurrent HTTP phases, messages under load, retained journals, delayed BODY visibility, reader failure, canceled bulk and a 30-second whole-provider blackout.

All required internal gates passed on this source: full core/server tests and vet, bundled IMAP checks, three repeated application suites plus a real-fsync added-delay suite, native TCP/IMAP bridge tests, focused race checks, Android domain tests, lint and both native ABIs. See verification.json for exact counts, hashes and scenarios. The full core suite has zero failed or skipped tests. Superseded optional load runners were removed; the current throughput case also passes inside the full build.

All six fresh SINGLE/MULTI cases passed. Each used 15 accounts, 80 ms command cost, a modeled 600 Mbps provider, 10 s warmup and 60 s duplex measurement with a 30 s tail. The user-authorized 10% tolerance requires >=90 Mbps in each direction and sustained tail, every loaded RTT <1,100 ms, and no timed-out/missed probes. MULTI uses 24 streams per direction. Each run passes independently.

| Case | Upload Mbps | Download Mbps | Maximum loaded RTT ms |
| --- | ---: | ---: | ---: |
| single-1 | 147.666 | 97.810 | 466 |
| single-2 | 154.285 | 90.116 | 513 |
| single-3 | 139.701 | 100.152 | 412 |
| multi-1 | 104.858 | 98.169 | 436 |
| multi-2 | 109.939 | 96.023 | 493 |
| multi-3 | 110.467 | 96.736 | 462 |

Install the debug APK and the Linux executable matching the server architecture. Use Rev4 on both endpoints and verify SHA256SUMS. The APK uses package org.itom.client.dev.rev7, version code 9204, and the published Rev3-testing signer; it can update that APK while preserving profiles and wallet history. Other signing certificates may require a separate migration. Preserve server configuration, journals, wallets and mailbox contents.

GitHub Actions assembles the release from hashed archive parts, verifies every artifact, checks that all release gates passed, and verifies uploaded asset digests before publication. Compilation and tests ran in the development workspace. These are laboratory checks; this release has not yet been tested on the actual Android phone/provider path. No raw user logs or private signing key are included.
