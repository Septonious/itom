# ITOM development archives and builds

[Download ITOM 0.9.2-Rev1](https://github.com/Septonious/itom/releases/tag/v0.9.2-Rev1): debug APK, Linux amd64/arm64 binaries, full archive, executable source ZIP, plan, verification evidence and checksums.

This is a transport implementation candidate. The account-count ceilings are removed, but sustained real-world 100/100 Mbps is still unqualified. See [verification](builds/0.9.2-Rev1/ITOM_0.9.2-Rev1_VERIFICATION.md) and [release notes](builds/0.9.2-Rev1/RELEASE_NOTES.md).

The APK uses a local debug key. The user chose a full local reinstall; an in-place update of the supplied Rev10 installation requires its original key. Both endpoints require Rev1/protocol 10. Keep current server quota journals and wallet state.

The original archive remains under `archives/`. New hashed archive parts and their manifest are under `archives/0.9.2-Rev1/`. `tools/publish_itom_rev1.py --output /path/to/new-directory` reconstructs the full ZIP and verifies each artifact. GitHub Actions performs this reconstruction and publishes the individual release assets only after integrity checks.
