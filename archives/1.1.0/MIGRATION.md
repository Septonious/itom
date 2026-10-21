# Development testing and migration

1.0.0 remains the frozen RC2 release. 1.1.0 DEV2 is experimental. A successful compile, unit test or local latency comparison is not a passed release qualification. The current DEV2 retained-state100Mbps target remains unmet; the20ms-server sensitivity also failed rate/RTT gates. Do not call those gates green.

The APK retains `org.itom.client` and increases the version code to10102. Configure all four existing `ITOM_ANDROID_*` signing secrets for persistent signatures. Without those secrets, build notes and `SIGNER.txt` identify a temporary debug signer. Do not uninstall/clear the app to work around an update signature mismatch: its private wallet and stable profile identity are required for historical accounting.

For the new2ms server, stop the old instance before transferring its private server identity/configuration and complete journal/state directory through your trusted private deployment channel. Preserve permissions, account bindings, folder ownership and user identity. Never run two writers against the same copied journals, publish private state in GitHub, or create a fresh empty journal to pretend old physical debt disappeared. Keep a consistent stopped-state backup for rollback. Server hardware/path assumptions require direct NOOP/APPEND/visibility and application measurements after transfer.

The transport wire format, journal schema, wallet semantics, grant scheduling and physical quota profiles remain the RC2 contracts. Unknown APPENDs, missing client wallet history and old unproved messages remain charged. The exhausted-fast-lease rollover experiment is preserved separately and is not applied in the current candidate. No release gate is waived.
