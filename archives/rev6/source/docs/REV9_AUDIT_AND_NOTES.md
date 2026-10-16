# Rev9 development notes

Revision: `transport-rework2-rev9`. The Android launcher and app-info display name is **ITOM**; the internal package identity is retained for continuity. The baseline is the exact released Rev8 source archive with SHA-256 `b354198b110134fd331d53fc3ceeadd5e899a359079392531c6dff594d0b4245`.

## Windows build repair

The uploaded native log showed `cannot use fd (variable of type int) as syscall.Handle` at two `syscall.Close(fd)` calls. The bridge now consumes the descriptor through `os.NewFile(...).Close()`, which compiles for both Windows host tests and Android. Host integration tests still run before gomobile bind. Native failures are reported at `android_native_build`, rather than `android_setup`.

Offline source-check tests keep intentional simulated failure/export output in individual logs under `dist/source-checks`. Real check failures are still fatal and shown in the console.

## Mobile interface

All displayed application text, notifications, validation explanations and server manager menus are English. Only historical migration fingerprints retain old localized strings as non-displayed data, so older vaults can still be migrated correctly. A source gate prevents new Cyrillic UI text.

Home drops its two top-right shortcuts and the transport subtitle. It shows a bounded, scrollable server announcement above the connection control. The announcement belongs to the active session and is cleared on stop or failure. On short windows or large system text, the home controls can scroll so a long announcement cannot squeeze the connection button out of the layout.

VPN networks is a separate settings page alongside SOCKS5, Notifications and Diagnostics. All switches use mint on and violet off. SOCKS authentication generates any missing username/password using SecureRandom; existing nonempty credentials remain stable. A generated password can be revealed in memory on the protected credentials screen.

Account cards show only upload, download, RTT, IMAP command latency, owned TCP streams and active transport lanes. Existing readiness/error coloring is preserved. The only export action is the full diagnostics ZIP; it is available with tracing off and includes the current snapshot and available traces.

## Server announcements

Menu item 17 and `itom message --text TEXT` update the current persistent message. `itom message` displays it; `itom message --clear` clears it. Message writes do not restart the service.

The existing authenticated control path carries a monotonic message revision and an acknowledgement. Delivery is scoped to each logical group under each registered client. The server polls the state once per second and retries unacknowledged messages no faster than every five seconds. Acknowledged revisions stop repeating. Same text is a no-op; the client ignores stale revisions and duplicate display updates. Losing an acknowledgement can require another transmission, which is deduplicated by the client. No dedicated control accounts are added.

Messages allow 2,048 UTF-8 bytes, newlines and tabs, and reject invalid UTF-8/control characters. Clearing the message is a new revision with an empty body.

Rev9 introduces an authenticated capability bit: update both endpoints. Existing profiles are retained. Message updates require configuration schema 4, which older managers reject instead of silently dropping announcements. Configuration edits remain atomic and locked. Do not downgrade the server manager after setting a message.

## Metrics and validation semantics

Per-account counters survive physical carrier reconnection within the client session. Rates count carried DATA payload, including repairs, rather than IMAP encoding overhead. TCP streams count ownership, so a stream is not counted once for every stripe carrying its data. IMAP latency is a moving average of successful fetch/append operations and excludes IDLE waits. Active lanes retain the existing two-direction readiness definition; they are not an IMAP socket count. Detailed tracing remains optional.

The bundle's validation reports record actual executed checks: 701 passed Go test events across 19 packages, mandatory outage/recovery checks, bundled IMAP tests, vet, both Linux builds, focused message/metrics race checks, Windows native host-test cross-compilation, native bridge host tests and both Android ARM targets. All 70 Kotlin domain tests passed; debug/release Kotlin compilation, fatal lint and verified debug APK assembly also ran. Device qualification remains separate. Native host tests and synthetic IMAP tests do not establish phone VPN/TUN behavior, live provider speed or field Speedtest reliability. No transport deadline, physical debt limit, cleanup proof or throughput gate is relaxed.

Backup accounts, a 4+1 allocation, and expanding the existing ten-account limit remain deferred. The ten-account limit is a configured implementation bound in Android validation, server configuration and qualification fixtures; it is not an IMAP protocol limit. A dedicated control account might reduce DATA queue contention, but can concentrate failures and quotas and distort RTT if probe and DATA paths differ. Any future experiment should keep separate control/DATA RTT estimates, allow control failover and compare loaded tail latency before reserving accounts permanently.
