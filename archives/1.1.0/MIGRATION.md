# Development testing and migration

1.0.0 remains the frozen RC2 release. 1.1.0 DEV1 is experimental. A successful compile, unit test or local latency comparison is not a passed release qualification. The retained-state100Mbps target remains unmet in the first comparison; one SINGLE loaded-RTT tail also exceeded750ms. Do not call those gates green.

The APK retains `org.itom.client` and increases the version code to10101. Configure all four existing `ITOM_ANDROID_*` signing secrets for persistent signatures. Without those secrets, build notes and `SIGNER.txt` identify a temporary debug signer. Do not uninstall/clear the app to work around an update signature mismatch: its private wallet and stable profile identity are required for historical accounting.

For the new2ms server, stop the old instance before transferring its private server identity/configuration and complete journal/state directory through your trusted private deployment channel. Preserve permissions, account bindings, folder ownership and user identity. Never run two writers against the same copied journals, publish private state in GitHub, or create a fresh empty journal to pretend old physical debt disappeared. Keep a consistent stopped-state backup for rollback. Server hardware/path assumptions require direct NOOP/APPEND/visibility and application measurements after transfer.

The transport wire format, journal schema, wallet semantics and physical quota profiles remain the RC2 contracts. Unknown APPENDs, missing client wallet history and old unproved messages remain charged. The new fast rollover only stops counting an exhausted message permission as usable; its old grant stays open and charged until the normal authenticated fence/proof lifecycle completes.
