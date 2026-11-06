# RC19 signed testing downloads

RC18 remains the first field-accepted candidate reliably meeting the goals.
RC19 is the diagnostics and idle control-polling follow-up; it needs field
confirmation. All fourteen matched jobs and independent application acceptance
passed. All fourteen exported files were downloaded again and verified.

| Package | Download |
| --- | --- |
| Signed Android APK, version code 10144 | [Android](https://github.com/Septonious/itom/actions/runs/42305783446/artifacts/13545823359) |
| Linux amd64 server .bin | [amd64](https://github.com/Septonious/itom/actions/runs/42305783446/artifacts/13545997601) |
| Linux arm64 server .bin | [arm64](https://github.com/Septonious/itom/actions/runs/42305783446/artifacts/13545548472) |
| Sources, evidence and checksums | [Sources/evidence](https://github.com/Septonious/itom/actions/runs/42305783446/artifacts/13545858356) |

These are GitHub Actions ZIP downloads, not a GitHub Release object. Extract
the APK/.bin before installation. Update both endpoints: install the APK over
the existing app; upload the server .bin into /tmp and use manager option 18.
No manual mailbox cleanup is required. Profiles and the APK signer are retained.

RC19 provides account rates even with tracing off, distinguishes established
application connections from overlapping account/tunnel stream counts, and
removes a redundant periodic NOOP from continuous control polling. Startup,
bulk pacing, limits, DNS caching, compact names and cleanup remain as in RC18.
UDP over TCP is possible and deferred at the user's request.

Three predeclared paired comparisons against RC18 preserved payload (geometric
ratios: upload 1.01857, download 1.00122). Idle medians improved in all three
pairs (combined ratio 0.9014). Loaded medians/p95 were mixed (ratios 1.0093/1.0143),
so no general loaded-RTT improvement is claimed. These are controlled probes,
not phone gaming measurements. Sub-100 ms remains unverified. All raw pairs and
strict misses remain in the evidence.

Source tag: `v1.1.0-mailstream-rc19`, commit
`c5c8d5eeb908448c72df91bc03ef455c7af41ac2`.
Build: [42303792500](https://github.com/Septonious/itom/actions/runs/42303792500).
Independent acceptance: [42303792301](https://github.com/Septonious/itom/actions/runs/42303792301).
Export: [42305783446](https://github.com/Septonious/itom/actions/runs/42305783446).

[Build metadata](MAILSTREAM_RC19_BUILD.json),
[download verification](ARTIFACT_EXPORT_VERIFICATION.json), and
[RC18 field acceptance](../mailstream-rc18/FIELD_ACCEPTANCE.md).
