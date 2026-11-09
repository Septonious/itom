# Build and install ITOM 0.9.2-Rev3

Download the APK and matching Linux server binary from the Rev3 GitHub release. Both server architectures are included: amd64 and arm64. Install the APK as `org.itom.client.dev.rev7`, version code 9203, version name `0.9.2-Rev3-debug`. The local debug signer is documented in `APK_SIGNER.json`; this environment generates a new debug certificate. An existing APK with another certificate requires a clean reinstall. Back up Android profiles you want to keep before uninstalling. No private signing key is distributed.

Preserve server configuration, journals and wallet state. Replace the server executable and restart using the existing deployment procedure. Do not reset quota debt, delete mailboxes or wipe journals for an upgrade. Run both Rev3 endpoints for comparisons. Verify downloaded artifacts against `SHA256SUMS`.

The executable source ZIP supports:

```bash
python ITOM-0.9.2-Rev3-sources.zip server --expect-revision transport-rework3-rev3
python ITOM-0.9.2-Rev3-sources.zip android --expect-revision transport-rework3-rev3 --accept-android-licenses
```

From extracted sources, with Go 1.27.1:

```bash
python tools/transport_qualification.py --go /path/to/go --accounts 15 --streams both --seconds 60 --warmup 10 --repeats 3
```

Run performance cases serially without builds or race instrumentation concurrently. Preserve failed cases and cold 1/5-second measurements. The strict 60-second case includes a 30-second final tail. The archive contains local qualification and build evidence; physical message throughput does not substitute for useful TCP throughput.

For field evaluation, repeat SINGLE and MULTI on the same phone, network and Speedtest server with 10 and 15 healthy accounts; also compare 5 or 8 if practical. Record exact UTC test start/end, mode, account count, app/server revision, useful speed and idle/download-loaded/upload-loaded RTT. Capture both endpoints throughout each run. Include fresh-start and back-to-back tests, concurrent bidirectional load, a temporarily unavailable account, and small application exchanges during load. Avoid changing the network or server between paired comparisons. The supplied screenshots have no mode/time markers, so their precise association with trace intervals cannot be recovered retrospectively.
