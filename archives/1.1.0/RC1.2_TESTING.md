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

The new runtime now writes the actual `transport-trace.jsonl` consumed by the
server trace viewer and diagnostic export, including events before any client
connects. It records IMAP setup stages, provider UIDPLUS/permanent-keyword
capabilities, original journal capacity/held counts, retained inventory failures,
standing rendezvous/request/invitation progress and session state. App/server
startup snapshots include the public configuration-binding hash for comparison.
No passwords, keys, request markers, provider response text or traffic bodies are
recorded. The trace rotates through three private files of at most 2MiB each.
Logging-off is honored; the live viewer shows the latest 30 events when opened.

IMAP keywords are case-insensitive. The prior implementation rejected provider
case normalization of its authenticated request marker, and could also mistake
a spelling change for a fresh VPN restart. RC1.2 accepts equivalent case only
after the original session/MAC validation, and deduplicates by the same session.
Tests reproduce the old rejection and carry real HTTPS/restarts through a
provider fixture that alternates keyword case. This is not a claim about the
exact response behavior of Rambler in the reported run.

Options 7/9 start or restart `/usr/local/bin/itom`; they do not install a binary
downloaded to another path. From the downloaded RC1.2 binary, use **option 11**
or its `install` command to update the service executable. Option 14 enables
server tracing; restart applies a changed logging setting. Option 12 displays
events and option 13 exports them, including failed-start records.

Try a connection with the server already running for at least two minutes.
Record the last startup hint if it stalls, then export app and server bundles.
After connection, test small HTTPS requests, SINGLE/MULTI throughput and one
user-initiated VPN restart.
