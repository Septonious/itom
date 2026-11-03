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
