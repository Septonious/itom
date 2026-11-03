# ITOM 1.1.0 MAILSTREAM-RC7 — automatic cleanup testing candidate

Update **both** endpoints. Upload the matched ITOM `.bin` to `/tmp`, then use
server manager option 18 or `sudo itom update-from-tmp`. Install the signed APK
in place, keeping its data and profile. No further manual mailbox cleaning is
part of this update. RC7 uses a fresh control namespace; do not mix with RC6.

Control messages are now collected automatically using stable UID cursors.
Abandoned partial DATA and old control folders are inventoried and emptied
within the configured identity's namespace. Verified-empty old folders are
removed while all sessions are idle. Leave the server running for that work;
immediate server shutdown defers remaining work to its next start.

Cleanup failures retain their charges and retry with backoff. Readers remain
independent of cleanup. A separate finite recovery-only reserve allows a full
old ledger to recover without allowing application traffic beyond its normal
budget. Diagnostics expose collection counts, abandoned-folder progress and
recovery failures. RC6 cancellation, half-close and native DNS/TCP fixes remain.

The release includes normal/race/native/SDK/vet, Android build/tests/lint,
verified HTTPS transfer and constrained-link evidence. Model success does not
guarantee live Rambler mutation behavior. If provider EXPUNGE permanently fails,
finite storage can still be exhausted; failed DELETE can leave empty folders
until its retry succeeds. Targets remain 300 ms idle/light RTT, 2000 ms loaded
RTT and sustained 100 Mbps each direction when the path permits. Strict ramp,
rolling-throughput and pause targets are reported individually, including misses.

For field testing, enable diagnostics before connecting, verify per-account
roles and RTT, browse, run a complete download/upload speedtest and reconnect.
Keep the server running after disconnect so idle cleanup can finish. Export both
endpoint diagnostics if transfer, reconnect or automatic cleanup fails.
