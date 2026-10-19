# ITOM 1.0.0 RC10.1 diagnostic handoff

Requested scope: add more precise latency profiling, then build the Android APK and server binaries. Baseline is the already packaged RC10. No performance tuning or qualification threshold changes are included.

Final build source commit: `76d08dc80a23f9322350ccdc570bad86b5768df6`.
GitHub Actions run: https://github.com/Septonious/itom/actions/runs/39255057487
Runtime: `itom-1-0-0-rc10-1`.
Android: `org.itom.client`, versionCode `10010`, versionName `1.0.0-rc10.1-debug`.

The first two intermediate workflow runs (39254732237 and 39254975244) are superseded. Use the final run's matched APK, binaries and source only.

The existing diagnostic exports now contain bounded per-account stage distributions and sampled correlated physical-batch timelines. See [profiler documentation](audit/PROFILER.md), [local verification](audit/LOCAL_VALIDATION.json) and [baseline hashes](audit/BASELINE_INTEGRITY.json).

RC10's unresolved qualification limits are retained and not waived. This package is for field testing and observation; it does not claim a new RTT/throughput result. All original performance qualification tools and thresholds remain byte-identical. Update both endpoints to obtain complete profiling; retain identity, configuration, wallets and journals.

Build artifacts, checksums and the full handoff are published under `downloads/`. All five final workflow jobs passed. The APK contains the selected revision in both ARM JNI libraries; 82 domain and 7 app JVM tests plus lint passed. See `audit/BUILD_VALIDATION.json`. No user diagnostics, credentials or private state are included in the handoff.
