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
issuer identity from the verified standing body. DATA offers now arrive through
the authenticated accounted session authority channel, rather than direct client
fixture acceptance. DATA issuer identities and selected-lane mapping now arrive
through authenticated roster negotiation; this is not complete production
initialization. Runtime handover waits for actual
BODY observation of every startup message, preserves bounded UID history across
replica absence and freezes bootstrap admission. Unknown writes retain their
exact charge and require storage proof; storage proof alone is not key confirmation.

## Integration required for the candidate

1. Connect the accounted rendezvous/invitation and independent key-exchange
   drivers and the implemented authenticated roster exchange to a production
   long-lived coordinator using the actual profile. Verify actual writable permanent keyword
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
3. Integrate the implemented grant/usage/fence/status/retirement channel with
   production multi-account policy, overlapping CONTROL/DATA renewals and
   old-session recovery. Scoped
   application-body proof reclamation and delivery receipts now exist and run
   under InboxWorker ownership. Cleanup now groups already-ready bodies into
   bounded original-proof transactions, reducing the variable-service backlog.
   Qualify sustained cleanup and receipt scheduling without weakening original
   BODY/absence proof or blocking receive service. Exhaustion must wake only after an actual durable
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

The latest control-container source checkpoint retains another overflow at the
same 64-message bound. Progress batching works without fill sleeps and reduces
three ready updates to one actual accounted mail in its regression, but cannot
stand in for sustained permission turnover and proof reclamation. Final checks
are 175/176 core groups, 10/10 native groups, 18/18 repeated targeted executions
and passing core vet; failed timed/capacity cases are kept in the evidence.

On resume, start with the production rendezvous/roster owner and original
DATA/CONTROL grant lifecycle, then native activation. Do not replace API stubs
with a superficially serving runtime lacking those authorities. Verify writable
permanent keyword support on the actual provider; the supplied trace has no
capability records. Keep the in-place journal/wallet paths, signing identity,
original ten-folder bound, two/three primary socket budgets and DNS defaults.


## Reclamation checkpoint

[Latest evidence](experiments/session-v2/RECLAMATION_CHECK_RESULTS.json) records
183/183 core race groups, 10/10 native groups, 30/30 repeated targeted executions
and core vet. All three original cold TLS/HTTP integrity cases pass. Their live
mailbox limit remains 64 messages/16MiB; deleted UIDs remain unreused holes and
actual inventory/sequence handling still executes. Peak fast-mailbox occupancy
is 24/52/55 in the final cohort. This resolves that observed short-case overflow,
not sustained operation: fluctuating cases still have 40/44 pending reclamations
at cancellation, and initial wallet grants retain original lifetime spend.

Reclamation requires original full BODY observation, successful logical delivery
(or discard of a closed stream), exact-set UID EXPUNGE, and a separate successful
same-UIDVALIDITY absence query. Server-origin mail also waits for an exact-body
client receipt; startup mail is excluded. Receipt keywords grant no quota and
cannot free logical send memory. Publication lifetime pins prevent an early
BODY/receipt from deleting a still-draining APPEND's completion row. The old
source failures and the intermediate test caller's undrained-FETCH-slot failure
are retained; the final caller respects the executor's existing tag-drain contract.

Production startup/roster, authenticated DATA/CONTROL grant turnover, persistent
profile reconciliation, native activation and original performance qualification
remain required. Efficient maintenance batching is now an explicit prerequisite
for a field release. Actual provider UIDPLUS/permanent-keyword support must be
verified. No RC, APK or deployable binary is produced by this checkpoint.


## Bounded cleanup groups

[Batch evidence](experiments/session-v2/BATCH_RECLAMATION_CHECK_RESULTS.json)
records 189/189 core race groups, 10/10 native groups, 21/21 repeated safety group
executions and core vet. With the same corrected provider fixture, both the
f7f6031 baseline and candidate pass nine cold scenarios. Median fast-mailbox
peaks in the variable-service cases improve from 53/52 to 24/23 messages.
Durations are mixed, so no general RTT or throughput gain is claimed. The same
64-message/16MiB live mailbox limit and application deadlines remain in force.

The selected batch consists solely of already-owned/applied candidates from one
issuer, capped at the original 32 proofs. Each original local publication pin
and per-body authenticated client receipt still applies. Exact UID sets exclude
intervening messages; only independently proven absent members can release
journal debt. Stale visible members remain charged and receive only later
absence probes. Uncertain or canceled commands retain their exact batch/handle.
No metadata marker or batch completion resets wallet spend or releases logical
DATA replay memory. Ready groups never wait to fill.

Efficient sustained receipt publication and cleanup scheduling still require
qualification. Production initial DATA/CONTROL authority turnover, profile and
READY/roster coordination, recovery, native integration and original performance
gates remain release blockers. These checks do not authorize an APK/server RC.


## Session authority integration

[Authority evidence](experiments/session-v2/AUTHORITY_CHECK_RESULTS.json) records
197/197 core race groups, 10/10 native groups, 33/33 repeated targeted executions
and core vet. DATA offers now travel through accounted Noise CONTROL mail. A
cold-runtime lifecycle fences a used grant, receives its replacement, observes
actual final cleanup, retires metadata through the protocol, rejects the retired
offer and continues serving TLS/HTTP. Descriptor pruning is checked against
original registered prefixes and lifetime spend; it is not a physical refund.

The [authority contract](SESSION_AUTHORITY.md) documents explicit policy APIs,
sender admission fencing, bounded memory, direction/scope checks, idempotent
issuance and retained uncertain work. Current routing pins one account and the
actual trusted ledger identities; it does not yet negotiate a production roster
or recover missing historical wallet state. Automatic overlapping CONTROL
renewal, multi-account coordination, profile/journal reconciliation, native
activation and all original performance gates still block an installable RC.


## Selected-lane roster integration

The [roster contract](SESSION_ROSTER.md) and [source check evidence](experiments/session-v2/ROSTER_CHECK_RESULTS.json)
replace the cold runtime's out-of-band DATA issuer mapping with an accounted,
Noise-authenticated offer/accept/READY exchange. Every actual message and the
final own publication must be observed/accepted before handover. Wallets open in
place; mismatched historical identity is retained and rejected, not reset.
Runtime adoption checks the exact negotiated lanes, ledgers and startup UID
history. Ten-account agreement keeps the original phone/server twenty/thirty
socket budgets and selects only the two implemented lanes per account.

Production profile/inventory coordination, missing-history recovery, automatic
overlapping CONTROL/DATA policy, native activation and full sustained performance
qualification remain required. Roster confirmation alone is not VPN CONNECTED or
permission to send DATA. This checkpoint produces no APK/server RC.


## Existing profile and issuer boundary

The [profile planner](PROFILE_SERVICE_PLAN.md) now validates original per-account
journal scopes, full retained folder inventory and shared participant budgets.
It creates no files and enables no admission. Separate installed issuer IDs cannot
be flattened into the current shared-store roster; the explicit adapter refuses
that conversion. Supporting those identities in the wire authority and original
client wallet paths is required before production startup can be activated.

Ten-account roster success must not be reported as compatibility with installed
per-account journals or as sustained multi-account DATA performance. Keep those
cases in the real startup/qualification suite when that owner is integrated.


## Distinct-issuer runtime assembly

[Per-account integration](PER_ACCOUNT_RUNTIME.md) now negotiates all original
account issuer IDs/runs, opens fixed local wallet paths in place and assembles
per-account authority/receive/publication/exit service under one runtime. The new
ten-account TLS/HTTP case uses twenty distinct stores and preserves old-session
wallet spend. Canonical OFFER chunks retain original packet/UID limits. This
resolves the shared-only wire/assembly gap; it does not activate production APIs.

The quiet-IDLE TLS failure reproduced in 1/10 runs. Bounded monotonic journal
update timelines are now available for causal correlation, including in-progress
updates without taking the database lock. Fsync and original deadlines remain.
Production persistent ownership/full inventory, missing-history recovery,
automatic overlapping CONTROL/DATA policy, native activation and all original
performance gates still block a working test release. Do not mark that TLS issue
resolved merely because a later cohort passes.


Fast server preparation now coordinates intent before durable grant reservation,
preventing redundant reservations/cancellations for one short flight. A retained
overlay baseline performs twenty metadata updates; the candidate performs two.
Bulk journal preparation remains concurrent in its separate regression. Source
CI runs these tests with real fsync and an explicit test-only delay hook. The
reported deadline failures remain part of the qualification boundary; this fix
and source checks do not enable packaging by themselves.


## Receive ownership and latency gate continuation

[Request-latency work](REQUEST_LATENCY_WORK.md) lets receipt/cleanup maintenance
progress while a receiver waits on IMAP, preserves exact bounded discovery hints
through cleanup and observes wallet/journal updates on one clock. A bounded
byte-level TCP/TLS route adds propagation/pipelining coverage to existing serial
command-service stress. A strict propagation light-load gate now runs in source
CI and retains failures. It is currently failed; production activation and all
original qualification requirements still block packaging.
