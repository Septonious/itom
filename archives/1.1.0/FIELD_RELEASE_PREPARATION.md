# Working field-test candidate

The requested target is one working APK/server pair from the replacement
transport, published on GitHub from the same source commit. The current branch
cannot supply that pair yet: `tunnel.StartClient` and `ServeManaged` still return
`V2_NOT_READY`. A binary compiled around those stubs is not a working test build.
Stable 1.0.0/RC2 remains separate; its removed runtime will not be reactivated as
the replacement transport.

## Runtime foundation now implemented

`transport.Runtime` assembles already scoped receive/publication workers and the
server TCP exit under one logical session owner. The caller supplies its actual
connection budget. Construction validates distinct physical executors, pinned
session/ledger identities, complete fast/bulk service and required protocol
authority before claiming any actor. It creates no additional sockets.

Workers cannot run outside their claimed runtime or be adopted while another
unlisted service is running. A read failure stops that carrier's new publication
admission; a write failure leaves receive service available. Other carriers keep
their own service. Exact unstarted/uncertain publications and original charges
remain reachable for an explicit recovery owner. There is no automatic resend,
account reset or invented permission.

Cancellation joins service under the existing command limits. Explicit final
runtime close shuts down the session and its owned sockets, while leaving wallet
and journal handles/state to their authority owner. Local `serving` state is not
authenticated READY or VPN CONNECTED.

The existing cold OPEN/TLS/HTTP scenarios now use this runtime rather than a
fixture-owned supervisor. They now establish and mutually confirm Noise keys through six physically
accounted control APPEND/FETCH exchanges on the same fast sockets. The standing rendezvous and its invitation now deliver real initial CONTROL
permission over accounted mail, using a bounded MAC-authenticated permanent
keyword request for the freshly generated wallet Session. The client learns the
issuer identity from the verified standing body. DATA offers still come from the
authority fixture; this is not complete production initialization. Runtime handover waits for actual
BODY observation of every startup message, preserves bounded UID history across
replica absence and freezes bootstrap admission. Unknown writes retain their
exact charge and require storage proof; storage proof alone is not key confirmation.

## Integration required for the candidate

1. Connect the accounted rendezvous/invitation and independent key-exchange
   drivers to a production long-lived coordinator and agreed READY/roster
   negotiation from the actual profile. Verify actual writable permanent keyword
   support; the uploaded field trace does not establish this capability. Establish server/store/run identities
   and real duplex permission before VPN CONNECTED. Account for bootstrap/control
   mail within the original separately reserved budget; neither an uncharged
   HELLO bypass nor fixture-issued grants can stand in for this protocol.
2. Open and reconcile existing journals/wallets in place, preserving every old
   grant, uncertain attempt, identity and signing key. Authorize additive v2
   folders without erasing retained legacy debt or treating missing wallet
   history as zero use. Respect the existing per-user folder bound even when an
   old four-shard profile already fills it; negotiate an eligible existing plane
   mapping or a proven safe capacity-neutral migration, rather than silently
   appending folders or creating empty replacement journals. Complete original
   trusted inventory before admission.
3. Connect authenticated grant/usage/retirement/recovery exchange and proof-based
   reclamation to the runtime. Exhaustion must wake only after an actual durable
   permission change. Unknown APPEND must retain its exact charged publication
   and use status/proof recovery, never a blind write retry.
4. Connect sender-fenced receiver windows and the measured-service controller,
   including shared-memory arbitration and agreed fast carriers. Preserve prompt
   short traffic while expanding sustained transfer service; return idle backing
   only after an authenticated sender fence.
5. Replace client/server API stubs with that integrated runtime and connect the
   native TUN/TCP path, server exit authorization, shutdown and causal diagnostics.
   Keep `org.itom.client`, the established signing certificate, and the completed
   UDP/TCP53 DNS race/session-latched IMAP fallback settings unchanged.
6. Run initialization, first-attempt TLS/HTTP, restart/reinstall history,
   cancellation, quota exhaustion, recovery and sustained transfer tests through
   the same entry points that the APK and server will execute. Use independent
   client/provider/server processes and calibrated network, command, CPU and disk
   service; keep held-out field traces separate from calibration.

These are dependencies within the single structural rewrite, not permission to
publish a series of partially wired RCs. Source checks remain necessary but
cannot authorize release by themselves.

## Acceptance and artifact boundary

Retain every required maximum, full measurement window, ramp/tail/rolling-rate
and useful-byte stall assertion. The targets remain <=250ms idle/light exchanges,
<=2000ms loaded probes and sustained >=100Mbps useful traffic in both directions,
including SINGLE/MULTI/duplex. Keep the original OPEN/TLS/HTTP limits and zero
unexpected TLS/Speedtest failures or load-induced carrier replacement. No failed
primary case is made passing by averaging, discarding an early run or reporting
physical APPEND bytes as application delivery.

The source host has a two-CPU quota. A retained runtime-integrated cold cohort
missed TLS startup while journal metrics recorded a commit of about 1.3 seconds.
That is a measured source-check concern, not proof of the cause of field failures
or an allowed extension of any deadline. Disk service must be measured explicitly
in qualification; fsync cannot be disabled or acknowledged before completion.

Once the integrated candidate passes its required checks, build the signed APK
and matching server artifacts from the exact checked commit. Verify package,
version, native transport revision and signing certificate; attach checksums,
source/qualification manifests and testing notes to GitHub downloads. Label field
evidence separately from synthetic qualification. No deployable artifacts have
been built or published by this preparation checkpoint.

Current startup source checks also retain an HTTP header-deadline miss and a
64-message fast-mailbox overflow. The added command/stage diagnostics must help
resolve these failures; increasing the mailbox/deadline or omitting failed
scenarios cannot qualify an RC. No deployable package was produced.
