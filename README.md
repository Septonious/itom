# ITOM development archives and builds

[Download ITOM 0.9.2-Rev2](https://github.com/Septonious/itom/releases/tag/v0.9.2-Rev2): APK, Linux amd64/arm64 binaries, full archive, executable sources, findings, plan and verification evidence.

Rev2 addresses durable publication costs, DATA-reader recovery/observability and cold-start feedback. Builds and required correctness tests passed. The full 100/100 Mbps performance qualification failed, especially MULTI; real Android/provider validation remains outstanding. See [findings](builds/0.9.2-Rev2/REV2_FIELD_FINDINGS_AND_RESULTS.md) and [verification](builds/0.9.2-Rev2/REV2_VERIFICATION.md).

The complete bundle, split archive parts and separately reviewable modified files are in [archives/rev2](archives/rev2). Preserve server journals and wallet state. The local debug signer matches the published Rev1 APK; clean local reinstall was authorized if needed.

## Previous Rev1 publication

# ITOM development archives and builds

[Download ITOM 0.9.2-Rev1](https://github.com/Septonious/itom/releases/tag/v0.9.2-Rev1): debug APK, Linux amd64/arm64 binaries, full archive, executable source ZIP, plan, verification evidence and checksums.

This is a transport implementation candidate. The account-count ceilings are removed, but sustained real-world 100/100 Mbps is still unqualified. See [verification](builds/0.9.2-Rev1/ITOM_0.9.2-Rev1_VERIFICATION.md) and [release notes](builds/0.9.2-Rev1/RELEASE_NOTES.md).

The APK uses a local debug key. The user chose a full local reinstall; an in-place update of the supplied Rev10 installation requires its original key. Both endpoints require Rev1/protocol 10. Keep current server quota journals and wallet state.

The original archive remains under `archives/`. New hashed archive parts and their manifest are under `archives/0.9.2-Rev1/`. `tools/publish_itom_rev1.py --output /path/to/new-directory` reconstructs the full ZIP and verifies each artifact. GitHub Actions performs this reconstruction and publishes the individual release assets only after integrity checks.

