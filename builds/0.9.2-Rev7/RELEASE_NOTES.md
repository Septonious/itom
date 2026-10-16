# ITOM 0.9.2 Rev7 — targeted validation prerelease

Repairs physical control-socket failure cascades while preserving encrypted sessions and application TCP streams; fixes critical LOGIN admission, missing handshake replies, retained-quota duplex admission and ordered multiplexed batching. The final fix prioritizes the sole DATA receiver and first usable DATA writer after correlated socket loss; extra writers remain optional.

At the user's request, broad/repeated tests were stopped to conserve usage. The earlier full suite passed 932 core test events before the final repair-priority fix. Final targeted recovery checks (including race detection), Android tests/lint and retained MULTI load/post-load checks passed. Existing and new application connections remained usable after load. The 90 Mbps floor was waived. A full final-runtime suite and live phone/provider validation are not claimed.

Update app and server together; preserve profiles and accounting state. The APK retains the Rev6 application ID and signing certificate. Use the AMD64 or ARM64 Linux binary matching the server. See `HANDOFF.md`, `VALIDATION.json`, `TEST_EVIDENCE.zip` and `SHA256SUMS` for exact scope, provenance and installation.
