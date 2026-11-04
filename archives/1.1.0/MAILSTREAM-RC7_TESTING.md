# ITOM 1.1.0 MAILSTREAM-RC7 — automatic cleanup testing candidate

Update **both** endpoints. Upload the matched ITOM `.bin` to `/tmp`, then use
server manager option 18 or `sudo itom update-from-tmp`. Install the signed APK
in place, keeping its data and profile. No further manual mailbox cleaning is
part of this update. RC7 uses a fresh control namespace; do not mix with RC6.

Control messages are now collected automatically using stable UID cursors.
Abandoned partial DATA and old control folders are inventoried and emptied
within the configured identity's namespace. Empty old folders are retained
because provider DELETE can lock an entire account. Idle control tails are
collected after sessions stop. Leave the server running for that work;
immediate server shutdown defers remaining work to its next start.

Cleanup failures retain their charges and retry with backoff. Readers remain
independent of cleanup. A separate finite recovery-only reserve allows a full
old ledger to recover without allowing application traffic beyond its normal
budget. Bulk estimation now excludes interactive probes and restarts after
application-limited gaps, fixing the second-transfer window collapse exposed by
the faster control path. Diagnostics expose collection counts, abandoned-folder progress and
recovery failures. RC6 cancellation, half-close and native DNS/TCP fixes remain.

The release includes normal/race/native/SDK/vet, Android build/tests/lint,
verified HTTPS transfer and constrained-link evidence. Model success does not
guarantee live Rambler mutation behavior. If provider EXPUNGE permanently fails,
finite storage can still be exhausted. Empty folders are harmless retained
containers; automatic cleanup never uses DELETE. Targets remain 300 ms idle/light RTT, 2000 ms loaded
RTT and sustained 100 Mbps each direction when the path permits. Strict ramp,
rolling-throughput and pause targets are reported individually, including misses.

For field testing, enable diagnostics before connecting, verify per-account
roles and RTT, browse, run a complete download/upload speedtest and reconnect.
Keep the server running after disconnect so idle cleanup can finish. Export both
endpoint diagnostics if transfer, reconnect or automatic cleanup fails.

## Verified final artifacts and results

Frozen source: `eb16f16a6f19257964977ce28881b89d90af4384`.
[Matched build and safety checks](https://github.com/Septonious/itom/actions/runs/41677382150);
[independent source acceptance](https://github.com/Septonious/itom/actions/runs/41677382308).

The signed APK retains the existing signing certificate and increases the app
version code to 10132. Both JNI libraries and both server architectures are
verified against the matched build. The actual downloaded amd64 server passes
six startup/reconnect cycles: three with 30/30 roles and three with 45/45.
Manual shutdown leaves the 20/30 physical-account counter entries at zero.

Automatic cleanup is replayed with both old ledgers full, retained partial DATA
and old control mail, 180 verified HTTPS requests, idle-tail collection and VPN
replacement. Unrelated mail is preserved. No DELETE is attempted. A lost
EXPUNGE outcome is quarantined and then audited successfully on retry. A thousand
tiny refund/reuse cycles add no ledger writes and still preserve crash debt.
Verified download, fresh-TLS upload and reconnect also complete when cleanup
stalls. Normal/race/native/SDK/vet checks passed in the final hosted gate;
Android passed 93 domain tests, 8 app tests and lint with zero errors.

These are verified HTTPS bytes over a TLS IMAP model, not live Rambler results:

| Streams / cycle | Upload Mbps | Download Mbps | Maximum idle/post RTT ms | Maximum loaded RTT ms |
| --- | ---: | ---: | ---: | ---: |
| 1 / 1 | 172.4 | 191.2 | 100.9 | 390.3 |
| 4 / 1 | 167.8 | 199.6 | 98.1 | 421.9 |
| 4 / 2 | 108.0 | 194.2 | 98.1 | 421.9 |
| 32 / 1 | 245.8 | 308.0 | 96.1 | 341.5 |

The 7 Mbps shared-wire case completes at 4.66 Mbps upload and 4.52 Mbps download, including download cancellation and fresh HTTPS recovery.

All final hosted cases pass complete-transfer, full-phase 100 Mbps (where the
wire permits), 300 ms idle/post RTT and 2000 ms loaded RTT checks. Strict rolling
five-second throughput and half-second-pause checks still have misses. This is
an experimental testing RC, not a claim that every performance target is met.

Earlier local attempts, including a frozen RC6 reference, recorded loaded-ping
timeouts. A local heartbeat observed a 1.8-second scheduling gap. The preserved
final local full-gate run did not complete before the workspace restart. These
results remain in the evidence and are not counted as passes; the mandatory
final-source gates above ran successfully on hosted runners. The final local
cleanup replay and downloaded-binary matrix passed after the workspace resumed.

The release includes the signed APK, both server binaries, source archive,
checksums, build/source manifests, artifact validation and sanitized evidence.
Keep the server running after VPN disconnect for idle cleanup. Empty folder
containers intentionally remain to avoid the provider's folder-deletion locks.
Real Android/mobile and Rambler confirmation remains necessary.
