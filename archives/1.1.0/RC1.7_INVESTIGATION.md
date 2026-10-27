# RC1.7 investigation: selected lines die before roster handoff

## Paired RC1.6 evidence

The October 27 client and server exports agree on revision and configuration
binding. The phone completes selection of all 20 lines in 1,044 ms. Its authenticated
request succeeds at the 31-character representation after explicit refusals of
the larger forms. Invitation, Noise and roster follow at 1,825, 2,934 and 4,308 ms.
The live client export at 32,002 ms is still CONNECTING/ROSTER, with no TUN or RTT.

Server UTC events show the actual failure boundary:

| Event | Time |
| --- | --- |
| Server starts | 17:03:33.852 |
| Initial inventory completes / rendezvous | 17:03:38.479 |
| Waiting for phone request | 17:03:40.330 |
| Invitation published | 17:05:31.945 |
| Noise | 17:05:32.929 |
| Roster entered | 17:05:34.374 |
| Failed | 17:05:34.376 |
| Generic PROTOCOL_OR_RETAINED_BODY recorded | 17:05:34.382 |
| Service restarted | 17:05:44.579 |

Thus the failure precedes RC1.6's DATA readiness check, rather than demonstrating
a DATA timeout. Journal counters still have headroom; this is not evidence that
quota is exhausted. Later waiting_request snapshots come from the restarted
server and cannot describe the failed roster's carriers.

## Structural defect and controlled reproduction

Inventory creates all selected executors before rendezvous. Only the anchor is
serviced while the standing server waits for a phone. The other selected sockets
can remain inactive indefinitely. Their read loops notice a close, but nothing
cancels the waiting endpoint. At roster construction, the closed socket is
reported as ErrProtocol alongside ownership violations. This loses the causal
distinction and leaves the phone waiting for its own existing startup deadline.

The new public-endpoint regression models a provider which expires an inactive
selected socket after 20 seconds and a phone which arrives after 25 seconds.
It runs actual TLS IMAP commands, production server/client entrypoints, real HTTPS
bodies, and wallet-preserving session restarts. Original startup, command, TLS
and OPEN deadlines remain intact. Before the fix it fails V2_STARTUP_ROSTER and
the server reports PROTOCOL_OR_RETAINED_BODY. With standby service it passes.
This reproduces the field failure class; it does not establish Rambler's actual
inactivity lease. No provider response identifying the exact cause was retained
in the supplied field trace.

The earlier public test delayed the phone for 91 seconds but had no provider
idle expiration. It could pass despite this defect. The new regression explicitly
models that missing behavior instead of merely extending a happy-path delay.

## Correction and ownership boundaries

Each nonanchor selected socket receives one private tagged NOOP at a ten-second
interval, bounded by one third of its existing command progress lifetime. The
anchor retains its existing active discovery. These workers cover rendezvous,
Noise and roster negotiation. They are stopped and joined, including completion
of any issued tag, before runtime ownership. No APPEND, credit, wallet, folder,
connection or login is added. A required lane closing aborts the startup owner
with SELECTED_LANE_CLOSED. There is no automatic reconnect or publication retry.

Only an internally marked, side-effect-free standby NOOP can span roster-driver
construction. Ordinary public commands still must drain. Unit regressions check
tag handoff, public-command rejection, cancellation/join and required-lane close
propagation. Parser/watchdog diagnostics retain fixed close codes for EOF, BYE,
read/write failure, timeout and intentional close. Cleanup cannot overwrite the
first close cause, and no provider text is exported. Stage carrier snapshots are
appended to the bounded rotating trace before restart can replace the live file.

## Earlier connected-session stop remains evidence, not a solved claim

The same server archive contains the older RC1.5 session which reached READY at
10:06:30.918 UTC and stopped at 10:06:34.375. Its a02 FAST executor failed with
the generic carrier category. Its inventory and expunge commands fail after
several successful appends. The trace did not distinguish EOF, BYE, command
timeout or intentional close, so it cannot establish a provider policy or prove
that standby maintenance also resolves every runtime failure. The new close
codes and retained stage snapshots address that observation gap.

## Qualification boundary

Passing this regression demonstrates that a standing endpoint survives the
modeled idle expiration and carries HTTPS. It does not prove the phone/provider
field problem is fixed, nor sustained 100 Mbps or the maximum 300 ms light-load
RTT target. No live provider or Android device is available in this environment.
The runtime controller, flow windows, cryptographic checks, exact cleanup proof,
durable historical debt and all original application deadlines remain unchanged.

Raw field archives are private investigation inputs and are excluded from release
assets. Published synthetic evidence retains the before-fix failure as well as
the passing after-fix cases and any other observed failures.
