# ITOM 1.1.0 RC1.1 — Android startup correction

RC1 rejected Android runtime profiles before making any IMAP connection: the
app supplies `kind: itom-android`, but the replacement core accepts the canonical
server profile `kind: itom-imap-probe`. Earlier public API tests constructed the
canonical profile directly and missed the Android/native adapter boundary.

RC1.1 translates the known Android discriminator at the native boundary, then
runs all original profile, capacity and security checks. Unknown kinds remain
rejected. A native regression reproduces RC1's `SERVER_CONFIG` failure and now
confirms that the real `New` entry point reaches the protected network path.

Startup failures now retain a bounded phase code (profile, wallet, IMAP,
rendezvous, secure handshake, roster or permission) in the themed error dialog.
With diagnostics enabled, a startup error is exported even when no native client
could be returned. Arbitrary provider/exception text is never copied into it.

Update the APK **in place**, retaining profile and accounting history. The wire
protocol and on-disk formats are unchanged from RC1; the RC1 server is compatible.
Matching RC1.1 Linux binaries are also provided for revision consistency.
Do not delete wallets, server journals or mailbox contents to troubleshoot.

This corrects a reproduced integration failure. It is not a new throughput or RTT
qualification. The existing 300ms idle/light and 2000ms loaded bounds and sustained
100Mbps up/down remain targets, with the misses and limitations documented in
[RC1 testing notes](RC1_TESTING.md). No application deadline, socket budget,
quota, receive window or congestion policy was changed for this correction.

After updating, enable diagnostics, attempt a connection and export both app and
server bundles if it fails. Record the phase code, whether all 20 selected lines
connect with ten accounts, a small HTTPS request, SINGLE/MULTI Speedtests and one
user-initiated VPN restart. No Android device or live Rambler pass is claimed by
the local regression or CI build.
