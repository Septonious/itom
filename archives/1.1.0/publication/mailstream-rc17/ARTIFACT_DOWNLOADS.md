# Verified RC17 testing downloads

All thirteen matched jobs, independent source acceptance and signed export passed.
All fourteen exported files were downloaded again and matched the expected hashes;
the downloaded APK passed v3 and persistent-certificate verification.

- [ITOM-MAILSTREAM-RC17-signed-Android](https://github.com/Septonious/itom/actions/runs/42248848474/artifacts/13519343549)
- [ITOM-MAILSTREAM-RC17-server-amd64](https://github.com/Septonious/itom/actions/runs/42248848474/artifacts/13518913685)
- [ITOM-MAILSTREAM-RC17-server-arm64](https://github.com/Septonious/itom/actions/runs/42248848474/artifacts/13518853797)
- [ITOM-MAILSTREAM-RC17-sources-and-evidence](https://github.com/Septonious/itom/actions/runs/42248848474/artifacts/13518104542)

Extract the ZIPs. Install the signed APK in place, upload the server .bin to
/tmp and use manager option 18. Update both endpoints together; no profile reset
or further manual mailbox cleanup is required.

Folders use only the compact itom- namespace: 31-character control names and
55-character DATA names, retaining full session identifiers. There is no legacy
migration. Healthy collection preserves RC16 amortization; a refused account
remembers bounded, resumable retries. Diagnostics capture coherent housekeeping
ownership. The two-writer limit, transport controller, DNS and Android signer
remain unchanged.

Controlled home-model results were about 130 Mbps upload and 205/188 Mbps
download across two runs. Six actual-server startup/reconnect cycles passed
at 30/30 and 45/45 lines, including verified stop-capacity resets. Android
passed 93 domain tests, 8 app tests and lint with zero errors, for both JNI ABIs.

Real-provider 100 Mbps uploads, VLESS parity and a two-second ramp are not
claimed. All strict misses, cold-debt behavior and the deliberately refused
cleanup model are retained in the evidence. RC10 remains the first field-ready
baseline; RC16 remains the immutable real-5G reference. These are verified
GitHub Actions testing artifacts.
