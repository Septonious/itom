# ITOM 1.1.0 RC1.2 — persistent listener and visible startup

RC1/RC1.1 applied the 90-second negotiation timeout before any phone request
arrived. A server started first could therefore exit before the app connected.
A separate-process public API regression reproduced this exactly: start the
server, wait 91 seconds, then connect the phone. The old server exited at 90s;
the corrected server completed the connection, actual HTTPS requests and a VPN
restart. The original failed log is retained with the local evidence.

The server now waits for an authenticated request for its configured service
lifetime. Its active handshake still has a 90-second bound; individual IMAP
commands retain their original progress deadlines. No accounting state is
reset, recreated or refunded by this lifetime correction.

The app now shows actual startup phases and authenticated mail SELECT counts,
for example **Connecting mail lines: 8/20** and **Contacting server**. Selected
sockets are not advertised as usable VPN lines. When tracing is enabled, a live
snapshot can be exported before the native startup call returns. The heartbeat
also lets a user cancellation interrupt native startup. A single 90-second
client budget now includes all account logins and negotiation; established
runtime lifetime is independent of that temporary deadline.

**Install both the RC1.2 server binary and APK. Restart the server service.**
Update the APK in place with the persistent signer. Keep the profile, original
wallets, server journals and mailbox state. RC1.1's Android profile adaptation
and the existing DNS behavior are retained.

These are startup and observability fixes. The existing 300ms idle/light,
2000ms loaded and sustained 100Mbps up/down performance targets remain
unqualified. No transport window, quota or application deadline was relaxed.
All limitations from [RC1 testing notes](RC1_TESTING.md), including incomplete
historical-body reclamation, still apply. No live Rambler or Android device
success is claimed by a local or hosted synthetic pass.

Try a connection with the server already running for at least two minutes.
Record the last startup hint if it stalls, then export app and server bundles.
After connection, test small HTTPS requests, SINGLE/MULTI throughput and one
user-initiated VPN restart.
