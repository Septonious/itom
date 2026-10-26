# ITOM 1.1.0 RC1 — first replacement-transport field test

Experimental APK and matching Linux server. This is the first installable
candidate of the independent session rewrite, not a stable or performance
qualified release. Install both ends; DEV/1.0 endpoints cannot speak this protocol.

The field trial is explicitly authorized with the current average lab results.
The 300ms idle/light maximum still has misses in retained lab cohorts; 250ms
remains preferred. Sustained 100Mbps up/down and the 2000ms loaded maximum are
targets, not established results. The earlier 200+Mbps component/model results
do not describe this APK's real application throughput.

## Install and retain state

Stop the old server service, back up its private configuration/state directory,
then replace only its executable with the matching architecture. Keep the state
directory, user registration, original `control.db`/`data.db` files and credentials.
Do not run the stable and testing servers against the same mailboxes concurrently.
Update the APK in place using the persistent signing certificate; do not clear
app data or uninstall to resolve a signature mismatch. Preserve the original
client `transport/<account>/wallet.db` files. The server menu and existing profile
export remain available. Provider connections currently require verified TLS
IMAP (normally port 993), UIDPLUS and writable permanent keywords. Unsupported
capabilities or incompatible retained mail fail explicitly at startup.

Ten accounts intentionally use **20 selected lines at each endpoint**, two per
account: one fast/control mailbox and one bulk mailbox. Unused socket allowance
does not create dummy lines to show 30/30. Startup inventories all retained
configured folders with bounded login parallelism before enabling admission.

The production entry points now perform accounted rendezvous/invitation, Noise
key confirmation, authenticated per-account roster negotiation and real initial
duplex permission. CONTROL/DATA allocations renew through the authenticated
channel within the original capacities. Updates retain original wallet spend;
only a surviving wallet can fence its old unused allowance. Missing history
remains charged. Retained historical bodies are read/authenticated and ignored
by the new epoch without deleting them or refunding physical debt. Comprehensive
old-body reclamation is not implemented; repeated reinstalls can exhaust retained
capacity. Preserve state and include both diagnostic bundles on startup failure.

## Testing and diagnostics

After startup, try a small webpage/message, three SINGLE and three MULTI
Speedtests, then restart the VPN once and repeat a short request. Record failed
starts as well as successful runs. Export the app's full diagnostics and the
server diagnostic ZIP before stopping the session. Server bundles now include
bounded `v2-<user>.json` startup/runtime snapshots and existing historical trace
files. They contain stages, command/queue timings, carrier state and accounting
progress, not credentials or plaintext traffic. An unavailable RTT is displayed
as unavailable; IMAP service time is not relabeled as tunnel RTT.

DNS remains the simultaneous UDP/TCP53 race to **31.131.251.83** and
**185.22.234.229**, sharing a 250ms deadline. If enabled, IMAP fallback begins
only after both fail and remains active until VPN restart. No DoH/DoT.

Current stream windows remain bounded at 256KiB per stream. Automatic grant
turnover is integrated; the experimental measured-service controller and
sender-fenced dynamic window helpers are not yet activated. SINGLE throughput
at longer RTT may remain constrained. This field test measures that limitation;
it does not claim a complete bandwidth rework or an improvement over RC2.

The source safety suite, native bridge checks, Python launcher checks and Android
compile/unit/lint checks accompany the build. Keep strict failed timing evidence
in `FETCH_OVERLAP_WORK.md` and its raw reports separate from functional checks.
No numerical performance threshold or application deadline was raised for RC1.
