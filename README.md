# ITOM

ITOM transports TCP application streams through authenticated IMAP mailboxes. This branch contains an **unstable Rev5 development checkpoint**, saved at the user's request during session network problems. Full current-source qualification is incomplete; this checkpoint is not a release.

The source, tests, intermediate results, validation/packaging drivers and detailed resume notes are preserved below. No Rev5 APK or binary is published by this checkpoint.

- [Rev5 source](archives/rev5/source)
- [Field analysis, causes and repair design](archives/rev5/source/docs/rev5/REV5_ANALYSIS_2026-10-15.md)
- [Current status and resume instructions](archives/rev5/checkpoint)
- [Checkpoint status](archives/rev5/checkpoint/STATUS.json)

Rev5 fixes the 180/192-message retained-debt grant deadlock, bounds continuously paced BODY reads and unread per-carrier queues, and sizes downstream envelopes using authenticated peer-delivery evidence. Scheduling waits expire across outages without releasing physical debt, and authenticated account recovery restores DATA writers without retaining cold exponential sleeps. It also distinguishes a retryable provider mailbox-state setup rejection from a stored-UID proof failure. Existing durable charges and cleanup proof rules remain intact. Server-manager messages support global text, per-client overrides and Minecraft-style colors; every global update clears the individual overrides.

The six latest focused ramp cases passed after the chat-classification and split-reader-affinity repairs. All required gates must still pass on the final source before publication. Darker Android UI polish and the authenticated admin console with durable 60-second connection-loss rollback remain pending. Irreversible purge and uninstall will remain CLI-only.

The user confirmed and reset the router that caused the earlier Wi-Fi bottleneck. The subsequent capture still records target connection failures; it does not establish the cause of the regular Speedtest-app error or contain the fatal stack for the previously reported whole-app mobile crash.

The prepared GitHub Actions workflow skips release assembly and publication until a packaged release manifest exists. For a future qualified package it reassembles the exact APK/binaries from hashed archive parts, verifies all assets and gate evidence, verifies uploaded asset sizes and SHA-256 digests, then publishes the prerelease. Compilation and qualification run in the development workspace. The private signing key, user captures, caches and stale build outputs are excluded.

Historical Rev2/Rev3/Rev4 archives remain available for comparison. Their individual field status and build identities are recorded beside each archive.
