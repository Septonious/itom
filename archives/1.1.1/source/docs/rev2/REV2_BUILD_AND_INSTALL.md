# Build and install ITOM 0.9.2-Rev2

The supplied APK is a locally signed debug build. The user explicitly authorized a full local reinstall, so rebuilding on the Rev10-key machine is no longer required. Android application ID is `org.itom.client.dev.rev7`, version code 9202 and debug version name `0.9.2-Rev2-debug`. Public signer metadata accompanies the build. No private signing key is distributed.

Use the APK and the Linux binary for your server architecture from the same Rev2 release. Back up any Android profiles you want to keep before the authorized reinstall. Preserve server configuration and all server journals/wallets: deleting quota state can forget accepted or uncertain physical mail. Do not purge server state to improve a speed test.

The wire protocol remains ITH10/ITP10/ITC10 with ITE2 DATA. Update both endpoints to Rev2 for a meaningful measurement of the changes. Rev10 transport peers are incompatible. Local durable schema and installed quotas remain compatible with Rev1; the new APIs change transaction grouping, not the authority required for refunds.

From the executable source archive, with Python 3.10+:

```bash
python ITOM-0.9.2-Rev2-sources.zip check --expect-revision transport-rework3-rev2
python ITOM-0.9.2-Rev2-sources.zip server --expect-revision transport-rework3-rev2
python ITOM-0.9.2-Rev2-sources.zip android --expect-revision transport-rework3-rev2 --accept-android-licenses
```

The launcher uses a user-selected local cache and verifies pinned tooling, bundled IMAP sources, runtime identity, mandatory tests and output identity. Performance measurements must run serially after builds, on unmodified source. The full archive includes the APK, Linux amd64/arm64 binaries, executable sources, verification logs, pseudonymized findings and checksums. Raw uploaded diagnostics, credentials, signing keys and worktree pointers are excluded.
