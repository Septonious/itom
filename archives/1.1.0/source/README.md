# ITOM 1.1.0 MAILSTREAM-RC5 — experimental testing release

The active Android bridge and server use `mailstream`. Both endpoints must use
this matched RC. Install the signed APK in place, preserving its private data
and profile. Update the server with manager option 18 (newest ITOM `.bin` in
`/tmp`) or `sudo itom update-from-tmp`.

RC5 replaces live DATA folder deletion and maintenance logins with two reusable
slots per account, session and direction. A reader validates a complete slot,
marks its exact consumed UIDs, uses standard CLOSE and verifies emptiness with
STATUS before confirming reclamation. No UID EXPUNGE or UIDPLUS is required.
Small traffic continues on the independent control path. The existing delivery
windows, congestion controller, Noise encryption and stream reliability remain.
A finite small-message reserve prevents old bulk from starving a fresh HTTPS
handshake after the delivery window shrinks.

Server-local capacity resets on manual stop/restart/update and an IMAP purge
attempt. That reset does not delete provider mail. Unexpected crashes preserve
uncertain debt. Old-session ownership and discovery-log retirement remain
limitations for sustained unattended use; this is a testing release.

The manager's account check now measures TCP/TLS/LOGIN/NOOP without selecting
legacy transport folders, and reports the failed stage. Account RTT and direct
IMAP latency remain separate measurements in diagnostics.

Targets remain 300 ms idle/light-load RTT, 2000 ms under load and sustained
100 Mbps each direction. Local TLS IMAP/HTTPS checks cannot establish these
results on Rambler or mobile networks. See [the transport contract](mailstream/REWRITE.md).

DNS races 31.131.251.83 and 185.22.234.229 over UDP/TCP 53 for 250 ms. Optional
IMAP fallback starts only after both fail and lasts until VPN restart. There
is no DoH or DoT. App theme and settings are unchanged.

Checks: `python3 tools/check_sources.py` and `python3 tools/build.py core --race`.
The latter includes real TLS IMAP, HTTPS, native SOCKS/TUN, restart, SDK and
pinned IMAP library checks. The separate qualification harness measures actual
verified application bytes with variable client/provider delay and shared wire
capacity. It does not report controller estimates as throughput.
