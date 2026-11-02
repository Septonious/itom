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
