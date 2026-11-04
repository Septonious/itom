# Mailbox stream transport contract — MAILSTREAM-RC8

RC5 field traces show nine client accounts failing at the first 32-message DATA
boundary while executing CLOSE, despite low direct IMAP latency. A separate
real TCP regression reproduces session replacement blocked by a silent origin
after request half-close. Native counters also expose worker refusals; a focused
regression proves that idle DNS associations could occupy every TCP worker.
RC6 addressed these independent failures; RC7 adds automatic retention recovery. The previous storage implementation
is preserved outside the active modules in `../../retired/mailstream-rc5-storage/`.

## Session and delivery

Fresh Noise NNpsk0 sessions bind the configured per-client 32-byte PSK and
protocol identity. Startup requires bidirectional encrypted DATA echo and a
mailbox ping. Any healthy account can establish contact; no anchor account,
wallet reconciliation, legacy mailbox scan or deletion gates readiness.

The pinned go-imap client owns response parsing and literal grammar. An active
endpoint has three sockets per account: control reader, DATA reader and APPEND
writer. A standing server identity keeps its control reader and opens the other
roles after authenticated admission. Provider admission includes all configured
identities. Same-user replacement joins the old DATA reader; the authenticated
silence lease handles abandoned peers independently of throughput control.

TCP streams have independent offsets and receive windows. Small writes dispatch
immediately; bulk batches only mail already queued. A separate bounded reserve
lets small DATA and fresh TLS proceed when earlier bulk exceeds a reduced
window. Bulk growth covers measured DATA-receipt RTT plus finite queue headroom.
A saturated sender probes by at most 50% once per complete observation round;
500 ms of extra DATA delay stops probing and restores the measured-rate bound. Interactive
DATA does not train the bulk estimator, and an idle/application-limited gap
restarts its observation interval. A faster light probe cannot pin a later
bulk burst to the initial flight window. Mailbox receipts do not release application-consumption credit. Control
uses absolute forward UID FETCH ranges and measured NOOP probes; DATA uses IDLE and bounded tail repair. Stream reset
and session cancellation close established exit sockets as well as mailbox
streams. Request half-close preserves ordinary response delivery.

## Reader-independent storage reclamation

A logical generation contains exactly 32 bounded MIME containers. Physical slots
are assigned independently of generation, announced over authenticated reliable
control, and never reused before verified collection. Normally a few slots
alternate; unavailable housekeeping may retain more, up to 64 slots per
account/session/direction and the unchanged 64 MiB per-account retained-byte
budget. The 512 KiB protocol reserve remains. A DATA-capacity wait must not
block control, APPEND completions, parking or shutdown.

After validating all 32 messages and their exact UIDs, the reader SELECTs a
session-specific parking mailbox, implicitly leaving the drained slot without
expunging it. It reports retirement and waits for the announced next slot. The
reader performs no CLOSE, DELETE, STORE or EXPUNGE, and never waits for physical
collection before moving to a fresh slot.

Only the server performs optional collection. For client uploads, its own
reader supplies the exact consumed UID/UIDVALIDITY proof and it accepts a
collection request only after client packet receipts and terminal APPENDs. For
server downloads, exact tagged APPENDUID results supply the ownership proof;
reader retirement, packet receipts and terminal tags are also required. A
missing APPENDUID prevents that outbound refund; it does not invalidate delivered
bytes. Sustained server-outbound reuse therefore needs UIDPLUS. Rambler advertised
UIDPLUS in the supplied traces; this is not assumed for arbitrary providers.

One server-wide lease fairly queues account owners and borrows an existing
APPEND owner after draining its tags. Eligible directions alternate, with oldest
records first within a direction, so upload debt and active download storage
cannot starve each other. Readers remember successful mailbox selection and
avoid repeated CREATE commands for the same session-owned parking/DATA folder.
Routing excludes that writer while leased; control and DATA readers remain
independent. SELECT verifies the owned slot's UIDVALIDITY and 32-message count.
Exact UID STORE, ordinary EXPUNGE and empty STATUS verification are pipelined.
All tags and unchanged UIDVALIDITY plus zero messages are required for refund.
The writer may remain selected: APPEND does not require unselection. No CLOSE,
UID EXPUNGE, folder DELETE or extra maintenance login participates.

An unknown or failed cleanup outcome opens a shared circuit breaker, preserves
all charges, and limits retries to the affected account/folder with exponential
backoff (2–64 seconds). Successful audited retry resumes the shared scheduler. Only the borrowed writer is
closed/reopened if necessary. It cannot cancel working readers or retire every
account. Parking preempts that writer's raw socket and joins its command owners
without treating expected cancellation as account failure. A reliable collection
acknowledgement allows the client to refund its matching ownership record.

## Native admission and diagnostics

DNS has a separate bounded 64-worker pool; TCP retains its 128-worker budget.
Answered idle UDP DNS associations release their worker after one second. Both
UDP and TCP port 53 use the DNS pool. DNS/TCP worker refusals and occupancy are
reported separately, alongside session-open refusals and cleanup circuit state,
in periodic diagnostics. RC7 also reports outstanding control messages, verified
control collection, abandoned folders emptied, storage recovery and
maintenance failures. `control_cursor` is a UID, not a sequence count. No targets, credentials or plaintext are added.

## Capacity and operations

Retention is durable and bound to physical account identity. Unknown APPEND
outcomes remain charged. Verified small refunds keep a conservative durable
high-water reservation; disk refunds are batched in 512 KiB quanta. This removes
per-prefix fsync without allowing unreserved publication, and clean close stores
exact remaining debt. A crash may recover the unused reservation as additional
conservative debt until verified namespace reconciliation. Control logs use UID cursors independent of shifting sequence numbers. Every
32 consumed messages, authenticated peer watermarks permit server-owned prefix
collection. Eligible control directions alternate so one cannot
starve the other. After historical recovery, optional control-tail maintenance
yields while bulk DATA is active and ordinary storage has headroom. It resumes
after a 300 ms DATA quiet interval; DATA reclamation, historical recovery and
control-byte pressure override this deferral. Incoming and outgoing consumed
control MIME bytes are tracked independently of DATA debt, so a full DATA
ledger does not divert its collection turns back to control-tail maintenance. Control uses the ordinary finite retained-byte budget and bounded actor queues.
It does not impose another count-based gate on receipts: provider cleanup
failure must not deadlock the acknowledgements needed to complete transfers.
Recovery messages have separate admission.
Exact APPENDUID bytes are refunded only after a UIDVALIDITY-preserving empty
prefix audit. Consumed prefixes retain short tails until a batch or idle sweep.

At epoch replacement, LIST recognizes only this configured identity's exact
old session and earlier control namespace grammar. Current-session folders and
unrelated mail are excluded. Old partial DATA and control mail are emptied and
audited before old aggregate debt is released; the client receives authenticated
recovery confirmation. No manual mailbox purge is required for normal recovery.
After all configured sessions stop and five seconds elapse, one optional login
collects control tails and empties abandoned folders. Admission
cancels it. No extra login runs while any session is active. Idle
inventory repeats every 30 seconds. Folder DELETE is never automatic, including
at idle: field evidence shows that this command can lock the whole account.
Audited empty folders remain, without retained message bodies. The server must be running to perform maintenance.

The 64 MiB ordinary budget stays fixed. A separate 64 KiB durable reserve carries
only small bootstrap and authenticated recovery updates, preventing a full old
ledger from blocking the very protocol needed to verify reclamation. It is not
available to application DATA or ordinary control. Persisted total retention is
strictly bounded by the sum; unknown APPEND charges remain until an eligible
verified collection. This does not prove arbitrary provider crash timing: live
provider mutation/visibility behavior still needs field confirmation. A provider that also stalls ordinary
EXPUNGE can continue delivery within finite retained capacity, but cannot sustain
unbounded transfers. Circuit isolation is not evidence that provider cleanup
works. These sustained-use limitations remain visible in diagnostics.

Manual server stop/restart/update and IMAP purge attempts reset server-local
counters, including failed purge attempts, as requested. This does not delete
provider folders, credentials or profiles. Unexpected crashes preserve debt.
Install the signed APK in place and update both endpoints; RC7 uses a fresh
control namespace. The existing profile and signing identity are retained.

DNS races 31.131.251.83 and 185.22.234.229 over UDP/TCP 53 for 250 ms. Optional
IMAP fallback begins only after both fail and stays active until user VPN restart.
No DoH/DoT or app theme/settings changes are introduced.

## Validation and limits

Regressions exercise the actual first-generation CLOSE stall, a second provider
that stalls all reclamation, full download then fresh TLS upload, aborted TLS,
reconnect with retained state, silent half-closed TCP origins, and native
SOCKS/IPv4 TUN under simultaneous DNS pressure and fresh HTTPS bursts. Existing
normal/race/SDK/vet and independently timed throughput/7 Mbps cohorts remain.
New regressions fill every old account ledger, seed retained partial DATA and
control, run 180 HTTPS requests during control EXPUNGE, stop and reconnect after
automatic offline collection, and verify unrelated mail is untouched. A lost
successful EXPUNGE outcome must quarantine charges, retry and audit without a
purge; recovery-only capacity must reject ordinary traffic.

Tests use verified TLS IMAP and real verified HTTPS bytes, variable propagation
and shared link capacity. Targets remain 300 ms idle/light RTT, 2000 ms loaded
RTT and sustained 100 Mbps each direction when capacity permits. Synthetic
success is not live Rambler or Android/mobile qualification. No field guarantee
follows from a short high-rate model run.

RC8 keeps 64 active streams and the original receive-memory bound. Excess local
OPEN calls wait within their caller context; close wakes waiters. Remote admission
withholds an OPEN receipt when a prior RESET is still in flight. Diagnostics
separate capacity waits from refusals and expose bulk receipt RTT, delivered rate
and flight saturation. This addresses the RC7 field burst without increasing
per-stream receive-credit allocations.
