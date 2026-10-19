# FIELD60 — public release name / safe rollback

Base: **the exact transport logic of FIELD57 (`transport-s1-perf7-field36`)**. No DATA scheduler, RTT policy, IMAP worker or wire changes. This release intentionally does **not** incorporate FIELD58/FIELD59 bulk selection experiments that coincided with serious download regression.

New public revision: `transport-s1-perf7-field60` and source `ITOM_TRANSPORT_FIELD60_SOURCE.zip`. Android: `0.9.0-dev2-field60[-debug]`, `versionCode=9060`, exported APK `ITOM_FIELD60_debug.apk`; server additionally exports `ITOM_FIELD60_linux_amd64` and `_arm64` as aliases (legacy `itom-linux-amd64` still works). Startup and trace report revision field60. Build refuses inconsistent source/Gradle/Go metadata and mismatched compiled Android APK.

**This is a packaging/version-gate and rollback candidate, not a performance improvement.** To verify installation, check the actual app build ID. Debug package `org.itom.client.dev` is different from release package `org.itom.client` and may coexist. Force-stop the correct app before testing; update server and client together. Record a clean diagnostic and server capture before attempting any new throughput optimization.

Windows: `python .\ITOM_TRANSPORT_FIELD60_SOURCE.zip all --expect-revision transport-s1-perf7-field60 --accept-android-licenses`

Server: `itom-linux-amd64 version` must print `transport-s1-perf7-field60`. If any gate fails, do not deploy. Never delete persistent tunnel journals/state during rollback.
