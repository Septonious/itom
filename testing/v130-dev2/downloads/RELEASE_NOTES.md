DEV2 fixes the startup stall reproduced from DEV1: a minute-long IMAP notification wait could outlast the app's 45-second connection deadline. Control traffic now uses bounded polling on matching primary receivers, and DATA has bounded tail repair when notifications are absent. A pending bootstrap request no longer recycles a healthy socket or appends more startup mail.

Update **both** the app and server. Same application id and signing key; existing profiles and mailbox state are retained. **No folder cleanup or profile reimport is required.**

The new notification-suppression regression fails on DEV1 and passes on DEV2. Verified: full Noise/TCP transfers and reconnects without notifications, variable-latency/network-replacement tests, multiuser and native SOCKS/TUN/DNS/SIGKILL recovery, transport race tests, 112 Android tests, lint, APK identity and signature. Failure diagnostics now name the IMAP operation and typed outcome, and configured line totals match four physical roles per account.

Assets: arm64 APK and statically linked Linux amd64 server only. Public-provider performance still requires field testing.
