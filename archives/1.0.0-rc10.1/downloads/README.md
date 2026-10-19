# ITOM 1.0.0 RC10.1 diagnostic build

[Android APK](ITOM-1.0.0-RC10.1-debug.apk) · [Linux amd64](ITOM-1.0.0-RC10.1-linux-amd64.bin) · [Linux arm64](ITOM-1.0.0-RC10.1-linux-arm64.bin) · [Sources](ITOM-1.0.0-RC10.1-sources.zip) · [Full handoff](ITOM-1.0.0-RC10.1-full-handoff.zip) · [Checksums](SHA256SUMS)

Build source: `76d08dc80a23f9322350ccdc570bad86b5768df6`. [Successful workflow](https://github.com/Septonious/itom/actions/runs/39255057487).

RC10 transport with bounded stage profiling in existing diagnostic exports; no performance tuning. Core safety and targeted profiling/race checks, 89 Android JVM tests, lint and artifact identity checks passed. Existing incomplete performance qualification remains incomplete. See [validation](BUILD_VALIDATION.json), [testing notes](TESTING_NOTES.txt) and [profiler documentation](../audit/PROFILER.md).

The debug signer differs from packaged RC10; see SIGNER.txt and testing notes before attempting an in-place update. Do not clear identity, configuration, wallet or journals to bypass a mismatch. Complete profiling needs the updated client and server.

On Linux, make the selected binary executable (`chmod +x FILE.bin`) and use the existing manager upgrade procedure. `FILE.bin version` must report `itom-1-0-0-rc10-1`.
