# Install ITOM 0.9.2-Rev4

Install the debug APK and replace the Linux executable with the amd64 or arm64 build matching your server. Use Rev4 on both endpoints. Verify downloaded files against SHA256SUMS and run the server binary's `version` command; revision is `transport-rework3-rev4`.

The Android application ID stays `org.itom.client.dev.rev7`; version code is 9204. This build reuses the Rev3 testing debug certificate. An APK with that certificate can update in place, preserving profiles and wallet history. Compare APK_SIGNER.json before installing over another APK. A differently signed installation needs profile backup and reinstall. No private signing key is distributed.

Stop/restart the server using the existing deployment procedure and replace its executable. Preserve configuration, journals, wallets and mailbox contents; do not wipe quota debt to make an upgrade appear healthy. Rev4 retains installed journal identities and supported capacities. Historical shard folders remain owned for inventory and cleanup.

Repeat SINGLE and MULTI on the same phone, network and Speedtest server. Include cold start, back-to-back tests, cancellation during load and a temporary connection interruption. After lanes rejoin, verify a new application connection plus message/media loading on an existing connection. Record UTC start/end, mode, account count, both endpoint revisions, useful speeds and idle/download/upload RTT; capture both endpoints through the full session. Keep raw diagnostic captures private.
