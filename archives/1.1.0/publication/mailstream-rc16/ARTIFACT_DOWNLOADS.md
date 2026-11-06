# Verified RC16 testing downloads

All twelve matched jobs, independent acceptance and signed export passed.
All fourteen exported files were downloaded again and matched expected hashes;
the downloaded APK passed v3 and persistent-certificate verification.

- [ITOM-MAILSTREAM-RC16-sources-and-evidence](https://github.com/Septonious/itom/actions/runs/42231795622/artifacts/13509236990)
- [ITOM-MAILSTREAM-RC16-server-amd64](https://github.com/Septonious/itom/actions/runs/42231795622/artifacts/13509112040)
- [ITOM-MAILSTREAM-RC16-server-arm64](https://github.com/Septonious/itom/actions/runs/42231795622/artifacts/13509062033)
- [ITOM-MAILSTREAM-RC16-signed-Android](https://github.com/Septonious/itom/actions/runs/42231795622/artifacts/13508957153)

Extract both ZIP downloads, install the signed APK in place, upload the server
.bin to /tmp and use manager option 18. Update both endpoints; no profile reset
or manual mailbox cleanup is required.

These are generated-fixture results. Real-provider 100 Mbps mobile performance,
VLESS parity, the mobile 45/40 goal in every phase and a two-second ramp remain
unverified or unmet as recorded. RC10 remains the immutable field-ready baseline.
