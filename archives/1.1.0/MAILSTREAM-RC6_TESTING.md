# ITOM 1.1.0 MAILSTREAM-RC6 — matched testing candidate

Update **both** endpoints. Server: upload the architecture-matched ITOM `.bin`
to `/tmp`, then use manager option 18 or `sudo itom update-from-tmp`. Install
the signed APK in place, preserving its data and profile. RC6 has a fresh control
namespace; do not mix it with RC5.

The supplied RC5 trace shows nine accounts dying in CLOSE at the first full
32-message generation. RC6 readers never perform cleanup: they select a parking
mailbox and move to a fresh authenticated slot. Optional server-owned ordinary
EXPUNGE is isolated from delivery, bounded and audited before any capacity
refund. A failed attempt pauses further cleanup; it cannot retire the readers.
No live CLOSE, UID EXPUNGE, folder DELETE or extra maintenance login is used.

Two independent fixes close established exit sockets when a stream/session is
canceled and isolate DNS workers from TCP workers. This addresses blocked VPN
replacement and reproducible TCP starvation by idle DNS flows. Diagnostics now
report cleanup circuit state, separate DNS/TCP worker refusals and session-open
refusals alongside mailbox progress.

The finite retained-byte budget remains unchanged, with at most 64 fallback DATA
slots per account/session/direction. If ordinary EXPUNGE fails too, retained
capacity can eventually run out; it is never silently refunded. UIDPLUS is needed
for sustained server-outbound slot reclamation. Old-session debt and append-only
control logs remain limitations. Manual server stop/restart/update/purge attempts
reset server-local counters as requested, without deleting provider mail.

For testing, enable diagnostics before connecting, confirm all account roles,
try browsing, then a complete download-and-upload speedtest followed by an
immediate VPN reconnect. Export both endpoint diagnostics if anything fails.
The low-rate/mobile and high-rate synthetic checks do not establish live Rambler
cleanup behavior or guarantee Android/mobile throughput.

Validation results and final build provenance are included with the published
candidate. The targets remain 300 ms idle/light RTT, 2000 ms loaded RTT and
sustained 100 Mbps each direction where path capacity permits. DNS settings,
app theme and persistent signing key are retained.

## Final verified model results

Every phase below verifies application bytes over HTTPS and TLS IMAP; rates
include TLS, ramp and drain. Client/provider delay varies independently and the
shared wire rate is bounded. These are model results, not Rambler/mobile results.
Four-stream tests repeat two complete upload/download pairs without restarting
the VPN, opening fresh TLS for the second pair. The provider-fault regression
separately exercises download first, then fresh-TLS upload, then VPN reconnect.

| Host / streams / cycle | Seconds per direction | Upload Mbps | Download Mbps | Max idle/post RTT ms | Max loaded RTT ms |
| --- | ---: | ---: | ---: | ---: | ---: |
| Hosted / 1 / 1 | 30 | 242.9 | 223.8 | 165.6 | 482.4 |
| Hosted / 4 / 1 | 30 | 245.7 | 221.2 | 181.0 | 441.0 |
| Hosted / 4 / 2 | 30 | 144.2 | 220.7 | 181.0 | 441.0 |
| Hosted / 32 / 1 | 12 | 252.7 | 277.7 | 181.8 | 453.0 |
| Local two-core / 1 / 1 | 30 | 228.2 | 218.0 | 164.8 | 618.3 |
| Local two-core / 4 / 1 | 30 | 239.1 | 220.3 | 190.7 | 414.6 |
| Local two-core / 4 / 2 | 30 | 140.9 | 209.3 | 190.7 | 414.6 |
| Local two-core / 32 / 1 | 12 | 212.2 | 258.3 | 153.1 | 394.8 |

The four-stream **7 Mbps shared wire** model completed at 4.62 Mbps
upload and 4.35 Mbps download. It canceled the download and then
opened fresh HTTPS successfully, with zero failed accounts, max idle/post RTT
192.9 ms and max loaded RTT 993.7 ms.

Final normal/race/native/SDK/vet checks passed locally and in hosted CI. The
actual downloaded server binary completed six native startup/restart cycles:
three at 30/30 roles and three at 45/45 roles. Manual server shutdown left all
20/30 physical-account counter entries at zero. Android compilation, 93 domain
tests, 8 app tests and fatal lint passed; the APK retains the existing signer
and both JNI libraries match the frozen source revision.

The hosted cases meet the release's complete-transfer, full-phase 100 Mbps,
300 ms idle/post RTT and 2000 ms loaded RTT checks. **Not every performance goal
is met:** strict ramp, rolling five-second throughput and half-second pause
checks still have misses, including in the repeated four-stream case. All flags
and failed intermediate runs are included in the evidence; none are counted as
passed. This is an experimental reliability fix for field confirmation.

One earlier two-core full race run hit the existing two-second fresh-HTTPS
probe timeout without a race report. Five unchanged focused runs, twenty
diagnostic repetitions and the final complete suite passed. No deadline was
relaxed. The earlier failure is retained in the evidence.

The RC5 failures are reproduced before the fixes: the first full-generation
CLOSE stalls download before upload; a silent half-closed origin blocks session
replacement; idle DNS associations can reject TCP workers. After the fixes,
24 MiB download, fresh-TLS 24 MiB upload and post-reconnect 24 MiB download
complete with zero failed accounts even when every cleanup attempt hangs.
The all-cleanup-fault case opens the shared circuit after one attempt and keeps
uncertain storage charged. The native SOCKS/TUN test also verifies 32 fresh
HTTPS posts alongside 128 DNS-association attempts and 16 incomplete TLS sockets.

Frozen source: `932496e4bcb7b0d44363f6c80f3bb9a4c50e4b92`.
[Matched build and acceptance run](https://github.com/Septonious/itom/actions/runs/41598390569);
[separate source acceptance](https://github.com/Septonious/itom/actions/runs/41598390673).
The release includes signed APK, both server architectures, sources, hashes,
artifact validation and sanitized model/regression evidence.
