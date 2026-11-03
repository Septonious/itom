# Mailbox stream transport contract — MAILSTREAM-RC6

RC5 field traces show nine client accounts failing at the first 32-message DATA
boundary while executing CLOSE, despite low direct IMAP latency. A separate
real TCP regression reproduces session replacement blocked by a silent origin
after request half-close. Native counters also expose worker refusals; a focused
regression proves that idle DNS associations could occupy every TCP worker.
RC6 addresses these independent failures. The previous storage implementation
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
window. Mailbox receipts do not release application-consumption credit. Control
uses measured NOOP polling; DATA uses IDLE and bounded tail repair. Stream reset
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
APPEND owner after draining its tags. Oldest eligible ownership records share
that owner across directions, so a download cannot starve upload refunds.
Routing excludes that writer while leased; control and DATA readers remain
independent. SELECT verifies the owned slot's UIDVALIDITY and 32-message count.
Exact UID STORE, ordinary EXPUNGE and empty STATUS verification are pipelined.
All tags and unchanged UIDVALIDITY plus zero messages are required for refund.
The writer may remain selected: APPEND does not require unselection. No CLOSE,
UID EXPUNGE, folder DELETE or extra maintenance login participates.

An unknown or failed cleanup outcome opens a shared circuit breaker, preserves
all charges, and stops further cleanup attempts. Only the borrowed writer is
closed/reopened if necessary. It cannot cancel working readers or retire every
account. Parking preempts that writer's raw socket and joins its command owners
without treating expected cancellation as account failure. A reliable collection
acknowledgement allows the client to refund its matching ownership record.

## Native admission and diagnostics

DNS has a separate bounded 64-worker pool; TCP retains its 128-worker budget.
Answered idle UDP DNS associations release their worker after one second. Both
UDP and TCP port 53 use the DNS pool. DNS/TCP worker refusals and occupancy are
reported separately, alongside session-open refusals and cleanup circuit state,
in periodic diagnostics. No targets, credentials or plaintext are added.

## Capacity and operations

Retention is durable and bound to physical account identity. Unknown APPEND
outcomes remain charged. Old/crashed sessions are not automatically deleted or
refunded; discovery logs remain append-only. A provider that also stalls ordinary
EXPUNGE can continue delivery within finite retained capacity, but cannot sustain
unbounded transfers. Circuit isolation is not evidence that provider cleanup
works. These sustained-use limitations remain visible in diagnostics.

Manual server stop/restart/update and IMAP purge attempts reset server-local
counters, including failed purge attempts, as requested. This does not delete
provider folders, credentials or profiles. Unexpected crashes preserve debt.
Install the signed APK in place and update both endpoints; RC6 uses a fresh
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

Tests use verified TLS IMAP and real verified HTTPS bytes, variable propagation
and shared link capacity. Targets remain 300 ms idle/light RTT, 2000 ms loaded
RTT and sustained 100 Mbps each direction when capacity permits. Synthetic
success is not live Rambler or Android/mobile qualification. No field guarantee
follows from a short high-rate model run.
