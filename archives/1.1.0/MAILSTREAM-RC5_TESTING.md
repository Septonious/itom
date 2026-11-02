# ITOM 1.1.0 MAILSTREAM-RC5 — matched testing candidate

Update **both** the server binary and APK. Server: upload the architecture-matched
ITOM `.bin` to `/tmp` and use manager option 18 or `sudo itom update-from-tmp`.
Install the APK in place; preserve app data and the current profile. RC5 uses a
fresh control namespace and must not be mixed with RC4.

The change removes live folder DELETE, UID EXPUNGE and extra maintenance
connections. Two DATA slots are reused only after exact consumption, CLOSE,
empty STATUS verification and authenticated confirmation. Packet receipts and
APPEND completion must also finish before capacity is released. Existing
throughput windows are retained. A bounded small-message reserve keeps fresh
TLS/HTTPS from waiting behind old bulk after a delivery-window reduction.

Manager option 6 now checks TCP/TLS/LOGIN/NOOP without touching legacy folders
and reports stage, reason and timings. Manual server stop/restart/update/purge
still clears server-local capacity. It does not delete retained provider mail.

For field testing: enable diagnostics before connecting, check per-account RTT
and IMAP latency, try ordinary browsing, then one speedtest and an immediate
VPN reconnect. Export both client and server diagnostics if anything stalls.
A provider account already blocked externally may require time to recover;
a local reset cannot unlock the provider.

This is experimental. CLOSE behavior on Rambler, sustained throughput and real
Android/mobile performance still need field confirmation. No universal speed
or RTT guarantee follows from loopback tests. Old-session debt and append-only
control logs remain sustained-use limitations.

## Verified model results

All Mbps below are verified application bytes divided by full phase time,
including TLS, ramp and drain. These are TLS IMAP/HTTPS models with independently
varying client/provider delay and a shared wire limit, not Rambler measurements.

| Host / streams | Seconds per direction | Upload Mbps | Download Mbps | Max idle/post RTT ms | Max loaded RTT ms |
| --- | ---: | ---: | ---: | ---: | ---: |
| Hosted / 1 | 30 | 253.7 | 307.9 | 174.4 | 415.9 |
| Hosted / 4 | 30 | 247.6 | 298.8 | 188.2 | 451.6 |
| Hosted / 32 | 12 | 218.6 | 266.2 | 178.9 | 389.8 |
| Local two-core / 1 | 30 | 167.4 | 216.7 | 237.9 | 842.5 |
| Local two-core / 4 | 30 | 216.2 | 178.3 | 277.7 | 1100.4 |
| Local two-core / 32 | 12 | 92.9 | 196.1 | 173.2 | 1155.8 |

The hosted 1/4/32-stream matrix passed all reported targets, including the
rolling throughput and pause checks. The slower local 32-stream short run
missed 100 Mbps upload; some longer local cases retained pauses and rolling
throughput misses. Both sets are included in the evidence. Host/model results
cannot guarantee provider or Android performance.

The local four-stream 7 Mbps wire-cap test completed cancellation and fresh
HTTPS recovery with no failed accounts and post-load RTT below 196 ms. Six
actual compiled-server/native startup cycles passed: three at 30/30 lines and
three at 45/45 lines, including server restarts and authentication/admin RPC.
All server capacity entries were zero after manual shutdown.

Normal/race/native/SDK/vet checks passed, together with Android's 93 domain and
8 app tests and zero lint errors. The APK retains the existing signing key.
The focused regressions verify 36 MiB in each direction plus reconnect with
DELETE/UID EXPUNGE poisoned, and fresh HTTPS while bulk exceeds a reduced
delivery window. Neither result establishes live Rambler CLOSE behavior.
