# ITOM 1.1.0 RC1.10 — live traffic ahead of historical recovery

This experimental build addresses RC1.9's PATH failure after successful login,
Noise and roster. It carries authenticated historical read knowledge into the
runtime, batches bounded historical authority work and lets short foreground
requests finish before launching more historical recovery. FAST reads and
receive admission also reserve service for live traffic. Optional cleanup
yields during short exchanges and resumes under bounded queue/quota pressure,
bulk load or quiet service; original physical debt and proofs are preserved. A new same-namespace backlog and surviving-wallet test
fails on RC1.9 and passes across two VPN sessions with the corrections.

Roster version 3 adds authority-only containers; update BOTH endpoints in place. Run the newly downloaded server executable,
choose option 11 to install that executable, then option 9 to restart it. Running
the old installed `itom` and choosing 11 reinstalls the old binary. Fresh trace
events must identify `itom-1-1-0-rc1-10`. Install the APK over the current app;
preserve app data, server profiles, wallets and journals.

READY still requires an authenticated DATA echo using original accounted OPEN
and DATA publications. Confirm a measured RTT and a small HTTPS site before
speedtests. If startup fails, export both full diagnostics while it is present.
The traces now include historical read hints and outstanding background work.

No quota reset/increase, deadline extension, extra connection, reconnect loop or
blind DATA resend was added. Historical hints cannot prove storage absence or
release debt. Full MAC, replay and exact cleanup requirements remain enabled.
DNS is unchanged: simultaneous UDP/TCP 53 requests to 31.131.251.83 and
185.22.234.229, shared 250 ms timeout, optional IMAP fallback latched until the
user restarts the VPN.

See RC1.10_INVESTIGATION.md and the complete passing/failing synthetic evidence.
Live connectivity and the sustained 100 Mbps / maximum 300 ms light-load RTT
goals remain unqualified. The active per-stream receive window is still 256 KiB.
