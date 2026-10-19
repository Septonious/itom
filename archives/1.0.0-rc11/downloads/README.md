# ITOM 1.0.0 RC11 downloads and handoff

Matched field-testing outputs from source commit `4de97a77ed5d0c2ff89e25212e78f5b840cfcade`. Subsequent commits add documentation and evidence only. Full release performance qualification is not claimed; thresholds remain unchanged.

- [Complete APK + Linux binaries + source + checksums package](https://github.com/Septonious/itom/actions/runs/39281400579/artifacts/12201108026)
- [Android APK archive](https://github.com/Septonious/itom/actions/runs/39281400579/artifacts/12201336633)
- [Linux amd64 and arm64 binaries archive](https://github.com/Septonious/itom/actions/runs/39281400579/artifacts/12201031853)
- [Exact build source archive](https://github.com/Septonious/itom/actions/runs/39281400579/artifacts/12200641952)
- [Source handoff and paired comparison](../source/HANDOFF.md)
- [Build validation](BUILD_VALIDATION.json), [SHA256SUMS](SHA256SUMS), [Android build report](ANDROID_BUILD_REPORT.json), [signer](SIGNER.txt).

GitHub artifact downloads require sign-in and expire on 18 November 2026. Source, audit evidence and manifests remain committed on this branch.

Update Android and server together. Preserve server state, quota journals, wallets and client identities. Android application ID is org.itom.client, version code 10011. The APK uses a debug signer; do not clear data or journals to bypass an update/signing mismatch. Certificate compatibility with a previously installed debug APK is not assumed.
