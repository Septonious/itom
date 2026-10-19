# ITOM1.0.0 RC10 for testing

- [Android APK](ITOM-1.0.0-RC10-debug.apk)
- [Linuxamd64 server](ITOM-1.0.0-RC10-linux-amd64.bin)
- [Linuxarm64 server](ITOM-1.0.0-RC10-linux-arm64.bin)
- [Executable source archive](ITOM-1.0.0-RC10-sources.zip)
- [Full handoff with source, APK, binaries and evidence](ITOM-1.0.0-RC10-full-handoff.zip)
- [Validation](BUILD_VALIDATION.json) · [Checksums](SHA256SUMS) · [Testing notes](TESTING_NOTES.txt)

Core safety/recovery/race checks,89 Android JVM tests, lint and APK/native identity verification passed. This is a testing build; RTT/ramp and higher-client-RTT throughput qualification remains incomplete. The production core is the same RC2-based checkpoint used in the paired comparison, with the reviewed retained corrections. No new controller/framing experiment is included.

Update both endpoints together and preserve private state. The binary opens the server manager without arguments. Verify hashes using `sha256sum -c SHA256SUMS`.

GitHub denied release creation with HTTP403, so permanent downloads are stored here. The full build also remains available in [Actions](https://github.com/Septonious/itom/actions/runs/39243128236).
