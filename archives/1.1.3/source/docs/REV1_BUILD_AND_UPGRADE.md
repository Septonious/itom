# Rev1 build, signer continuity and upgrade

Date: 2026-10-14. Release: 0.9.2-Rev1, revision `transport-rework3-rev1`.

## Android: use the machine holding the Rev10 key

The user selected a local build on that machine. No private signing key was present in the supplied archive; the public Rev10 certificate is not sufficient to sign an APK. The subsequent full build bundle includes a local debug APK for testing, with signer SHA-256 `c9c766e99b9b691044bd7fd2a5613ceb15fb3266f87aee225a84f56f160a8e45`. It is not signed with the Rev10 key and cannot update that installation in place. Sign with the actual installed app's original key before updating; keep profiles and app data.

1. Copy the source ZIP to that machine and verify its SHA-256 against the delivery checksum file.
2. Keep the existing Rev10 installation and saved profiles. Do not uninstall the app or clear its data.
3. If Rev10 used that machine's standard Android debug keystore, run the following in its normal user session so Gradle reuses the same keystore. Use the existing cache path if desired; do not redirect Java user.home to a different account.

```bash
python ITOM-0.9.2-Rev1-sources.zip android --expect-revision transport-rework3-rev1 --accept-android-licenses
```

The APK and build report are exported beside the ZIP into `dist`. The build runs Go/native verification, Kotlin domain tests, compilation, lint, identity and native-library checks. A compiled APK is not a device performance qualification.

If the original key is a custom keystore rather than Gradle's standard debug key, sign the locally built APK with that key using the SDK build-tools `apksigner`. Use its interactive password prompts; keep the key and passwords on your machine. Example (replace paths and alias with your local values):

```bash
apksigner sign --ks /path/to/rev10-keystore --ks-key-alias YOUR_ALIAS --out ITOM-0.9.2-Rev1-local.apk dist/ITOM-0.9.2-Rev1-debug.apk
apksigner verify --verbose --print-certs ITOM-0.9.2-Rev1-local.apk
```

For the standard-debug-key build, verify `dist/ITOM-0.9.2-Rev1-debug.apk` instead. The SDK build-tools directory may need to be added to PATH.

Expected Rev10 signer certificate SHA-256 (from the supplied public certificate evidence):

```text
46e3e8f2f3098fa9e5bc9231a7403e2b0ba0ee72c34e1565ff16d5484ce4dee3
```

Verify this against the certificate of the actual installed Rev10 APK as well. The application ID must be `org.itom.client.dev.rev7`; the new version code is 9201. If the signer differs, stop and sign with the actual installed app's key. Do not resolve a mismatch by uninstalling or clearing data. After verification, install as an in-place update (`adb install -r YOUR_VERIFIED_APK`, or the normal Android installer). Android/device update and profile-retention behavior were not exercised here.

## Server and paired-state upgrade

Protocol 10 requires both ends to be Rev1. Plan a stopped upgrade window; keep the existing server configuration, physical journals, account IDs, user PSKs, logical identities and Android profiles.

Before running Rev1, stop the server service and disconnect clients. Back up the complete stopped server state (configuration, quota journals/wallets and related files); preserve client-side state using the supported profile/state backup facilities. Record binary and APK identity/signers. Do not send backups containing credentials to public logs or the source bundle.

Verify the supplied Linux binary checksum, make it executable and check its version. Choose amd64 or arm64 for the host. The manager's install operation performs binary replacement and may start/restart the service, so finish both-endpoint preparation and the stopped-state backup before invoking it.

```bash
sha256sum -c SHA256SUMS
chmod +x ITOM-0.9.2-Rev1-linux-amd64
./ITOM-0.9.2-Rev1-linux-amd64 version
sudo systemctl stop itom
sudo ./ITOM-0.9.2-Rev1-linux-amd64 install
```

Keep the original state-directory selection if customized. Start/enable clients only after both endpoints identify as `transport-rework3-rev1`. New DATA-folder scope is added with an audit; installed physical capacity is not multiplied by folder count. Existing durable quota debt remains recorded. No remote service was installed or deployed during this work.

## Rollback boundary

Before Rev1 has published any new physical DATA, a stopped rollback can restore the paired pre-upgrade binaries and matching stopped state.

After Rev1 publishes DATA, do not restore older journal/wallet snapshots: doing so would forget possible physical exposure and unknown-outcome debt. Do not automatically restart Rev10 against newly extended state. Stop traffic and keep the current journals; downgrade then requires a separately reviewed, exposure-preserving migration/drain procedure. This delivery does not provide such a downgrade tool. Android normally rejects lower version codes as an in-place downgrade; do not bypass that by uninstalling an app containing saved profiles.

## Field acceptance still required

Run the laboratory runner first, preserving every failed and passed result. Then validate the actual Android/provider path with useful TCP upload/download, loaded application probes and repeated SINGLE/MULTI tests, using matched account sets and provider conditions. Include low-session-limit and heterogeneous-account cases, delayed UID visibility, whole-provider outages, reconnects and a long soak. Separate application RTT from IMAP command latency and DATA acknowledgment age. The target is sustained 100/100 Mbps and every loaded probe below 1000 ms; no synthetic Go result certifies that target in the field.
