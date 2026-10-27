# ITOM 1.1.0 RC1.7 — service selected mailboxes while waiting

This experimental build fixes a reproduced startup failure: unused selected
IMAP sockets were left idle while the server waited for the phone. A provider
could close them before roster handoff. The server then failed immediately at
ROSTER while the phone waited for an exchange that could no longer complete.

The supplied RC1.6 logs show all 20 phone lines selected, followed by a server
roster failure about two milliseconds after entering that stage. Its sockets had
been selected almost two minutes earlier. The logs do not identify the provider's
exact close reason or inactivity policy. A synthetic provider with an inactivity
lease reproduces the same stage and failure class before this fix; afterwards it
completes startup, real HTTPS and wallet-preserving restarts.

Already selected nonanchor sockets now receive a tagged NOOP every ten seconds
while the endpoint waits and negotiates. The active anchor continues its existing
polling. No extra connections, logins, folders, DATA messages or permissions are
created. A keepalive tag drains before runtime takes ownership. A closed required
socket cancels startup with SELECTED_LANE_CLOSED instead of a generic roster
protocol error. This does not reconnect or retry ambiguous publications.

Server stage snapshots are retained in the rotating trace, including carrier
state, rather than being lost when a restart overwrites the latest snapshot.
Carrier diagnostics distinguish provider BYE, peer EOF, read/write failure,
command timeout and intentional owner close, using fixed codes without provider
text. The first close cause survives cleanup. Existing command deadlines and
unknown-publication accounting are unchanged.

Update BOTH endpoints. Install the APK in place; retain profiles, wallets and
journals. Run the newly downloaded server executable, choose option 11 to install
that executable, then option 9 to restart. Running the old installed `itom` and
choosing 11 reinstalls the old executable. Fresh events must identify
`itom-1-1-0-rc1-7`; menu 12 initially replays historical entries.

Let the server stand for at least a minute before connecting. READY still
requires the RC1.6 authenticated DATA nonce echo, and the app should show an RTT.
Try a small website before SINGLE and MULTI tests. If it fails, export both full
diagnostics while the failure is present. No reinstall is needed.

The inactivity regression is a controlled reproduction, not a measurement of
Rambler's timeout. No Android device or live provider is available locally.
Sustained 100 Mbps and maximum 300 ms light-load RTT remain unqualified; RC1.6's
11/16 synthetic latency-gate result remains historical evidence, not a passing
claim for this build. This release changes startup connection maintenance and
diagnostics, not the runtime congestion controller.

DNS remains unchanged: race 31.131.251.83 and 185.22.234.229 over UDP/TCP 53,
250 ms shared direct timeout, optional IMAP fallback latched until VPN restart.
