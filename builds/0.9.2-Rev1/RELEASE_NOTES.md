ITOM 0.9.2-Rev1 transport rework candidate, revision `transport-rework3-rev1`.

Includes the debug APK, Linux amd64/arm64 binaries, full archive, executable sources, rework plan, verification report and checksums. Configured account-count ceilings have been removed; active provider sessions, login concurrency and memory remain bounded.

The final local Go build gate passed 758 test events with zero failures and 13 optional skips. All three required outage-survival cases executed and passed; vendored IMAP tests and vet passed. Android native/Kotlin/compilation/lint/identity checks passed (71 domain tests in the verified reports). Both Linux builds reproduce the previously verified binary hashes.

The APK is signed with this environment's debug key. The user elected a full local reinstall; it cannot update the supplied Rev10 APK in place. No private signing key is included. Both endpoints require Rev1/protocol 10. Preserve server quota journals and wallet state when updating.

**Sustained real-world 100/100 Mbps remains unqualified.** The available pre-final laboratory run measured 103.049/78.731 Mbps with 449 ms maximum loaded application RTT; download missed the target. Final-source repeated SINGLE/MULTI, real-provider/device trials and soak remain open. Go correctness passes do not imply field performance.

Large upload files were transported as hashed parts because the connector limits requests to 16 MiB. GitHub Actions verifies the parts, complete archive and extracted artifacts, then checks every uploaded release asset's size and SHA-256 before publishing this prerelease.
