# Replacement transport implementation status

This branch is an **unqualified implementation in progress**, not a runnable RC
or achievement of the 250ms/100Mbps targets. Production client/server startup
still returns `V2_NOT_READY`; packaging is disabled. No APK or deployable server
binary is published from this branch. This is one structural replacement, not a
series of intermediate timing releases.

The removed runtime is preserved at `d16796d85b91b1bfe091ad0f24c151ba2f0429a3`.
The first independent implementation is `fb22eba`. Removal manifests retain paths
and SHA-256 hashes. Historical scheduling, timers and congestion behavior are
not implementation or test oracles. Cryptographic/accounting contracts remain
mandatory independently of that implementation.

## Implemented

* Session-owned bounded send/reorder queues, separate fast/bulk dispatch and
  receiver-backed memory credit. Dispatched bytes stay retained until cumulative
  authenticated peer receipt, rather than disappearing on APPEND acceptance.
* Event-driven application `net.Conn` adapter: blocking reads/writes, mutable
  deadlines, independent cancellation, half-close, FIN/RESET acknowledgments,
  bounded retirement and retired-ID rejection. App closure does not close a
  provider socket. Tests exercise 2,000 short connection lifecycles.
* Replenishing per-flow and aggregate interactive budgets replace the initial
  lifetime burst counter. A continuing 320 B/s flow remains fast across 10,000
  exchanges; opening more flows cannot multiply the fast budget. Excess DATA
  continues on bulk immediately, without waiting to fill a collector or refill.
* Receipts and credit are coalesced; DATA replies can piggyback both. Short
  responses need not create a separate physical control solely for feedback.
  Reserved dispatch slots keep bulk uncertainty from consuming protocol capacity.
* New verified TLS/LOGIN/CAPABILITY/SELECT bootstrap and pinned UIDVALIDITY.
  The bounded single-parser executor overlaps APPEND with UID FETCH, supports
  synchronizing literals, LITERAL+, and LITERAL- up to its 4096-wire-byte limit.
  Unsolicited flags-only FETCH does not break BODY completion ownership.
* Independent v2 framing/Noise domain with the original nonce, replay and key
  usage safeguards. The unqualified prototype framing now includes receipt and
  credit fields. It has never been deployed as a compatible production wire.
* Concrete-grant admission fits exact encrypted-envelope/canonical MIME size
  before encryption and durable spend. Publisher construction rejects a wallet
  from a different authenticated session. DATA and control use explicit separate
  ledger classes even when sharing fast mailbox service.
* Bulk aggregation drains ready DATA immediately into bounded multi-record
  bodies, under one exact physical grant/ordinal. It neither waits to fill a
  body nor borrows short fast-plane work. A partially available concrete grant
  can admit a smaller body without fencing that grant. The existing physical
  byte/record ceilings are unchanged.
* Whole-body batch authentication stages replay advancement until every record
  has authenticated and passed scope/ledger validation. Failed groups expose no
  plaintext and consume no replay slots, while successful decryptions still
  count against the original hard key-usage limits. Delivery retains progress
  across temporary capacity pressure and discards a closed stream independently.
* Definitely unstarted bursts remain reachable beyond one dispatch window;
  retry metadata is bounded by retained DATA and coalesced per-stream controls.
  Unknown publications never enter this retry queue. Retirement removes stale
  retry entries without changing physical debt.
* Client publication path through the new executor, plus server client-origin
  BODY authentication/accounting/delivery. A missing descriptor prefix defers
  before consuming an AEAD replay slot. A verified delivery object can wait for
  logical receiver capacity without decrypting the BODY a second time.
* Remote descriptor registration and owned BODY observation now share one
  durable transaction using the same grant/proof validators. Failed proof rolls
  back both; a lost commit acknowledgment preserves both after reopening. No
  receipt is returned before the commit and no refund occurs in this method.
* Unknown publications cannot be automatically re-APPENDed. A command which
  never started can retain its exact prepared body/permit. Trusted local FETCH
  of the exact body can confirm provider acceptance, but cannot invent peer
  delivery, release logical replay bytes, or refund physical quota.
* Server-origin publication and client reception now have independent physical
  origin/ledger authentication. The client pins the issuer store/run, account,
  user, folder, UIDVALIDITY, Noise session and record generation before delivery.
  Server descriptors cannot become remote client grant reports. Protocol and
  DATA authorities remain distinct even on a shared fast mailbox.
* Local server admission fits a concrete one-body reservation to BOTH account
  and protected user availability, including retained remote grants. Body intent
  and unused-grant close commit atomically before I/O. A lost commit reply grants
  no writer permission; committed exposure survives reopening as unknown debt.
  Server APPEND outcomes and trusted exact FETCH proofs update the local journal
  without inventing peer receipt or physical reclamation.
* Prepared publication and storage-proof calls require an executor bound to the
  correct profile account and selected mailbox. Unbound low-level executors can
  run protocol tests but cannot publish an accounted body. Mismatched routes fail
  before any IMAP command; the exact prepared body/permission stays usable on the
  correct route without an additional spend.
* Receiver-trace checks retain the required full-phase/ramp/tail/rolling rate,
  no-stall and RTT-maximum assertions. No peak-only or successful-only score is
  accepted as qualification.
* Authenticated OPEN/OPEN_OK/OPEN_ERROR replaces manual stream creation in the
  new client dialer and bounded server exit workers. Targets have canonical
  framing, authenticated-user authorization and a pinned numeric dial address.
  The original 1500ms OPEN limit is unchanged. Cancellation, policy denial,
  out-of-order early RESET and uncertain OPEN retirement do not replace carriers.
* Exact grant admission includes variable OPEN payloads and immutable unstarted
  retries. Neither can exceed its concrete payload allowance or spend DATA
  authority for protocol work. A validated RESET_ACK can retire an uncertain
  logical OPEN without refunding its physical publication.
* Bounded scoped inbox enumeration uses actual UIDs rather than UIDNEXT or a
  high-water cursor. UID holes and late lower UIDs remain discoverable. Dependency
  gaps defer before decryption; a backpressured authenticated delivery is retained
  without decrypting or fetching it twice. Solicited metadata produces no
  self-poll notification. Inventory absence is never financial release evidence.
* Server mailbox decoding now classifies client/own origins and DATA/protocol
  stores under pinned account/user/session/folder/incarnation. Own publications
  correlate through a bounded journal index rebuilt from original attempt rows
  on startup, including historical unknown debt. Exact BODY observation is still
  mandatory; lookup, ambiguity and index absence never authorize refund or retry.
  The index changes atomically with body intent/deletion and tolerates compatible
  writers which do not maintain derived metadata.
* Direct DNS settings race 31.131.251.83 and 185.22.234.229 with one 250ms deadline,
  basic UDP/TCP53 only. Optional IMAP failover latches after both direct paths
  fail until user VPN restart. Native and domain checks are recorded separately;
  full Android UI build/device validation and transport activation remain pending.

## Verification boundary

All old owned Go tests were removed after the first implementation, as requested.
Their manifests and immutable Git history remain; third-party tests and release
archives were untouched. Fresh scenario and invariant tests are independent.

Application/TCP integration runs real TLS/HTTP and a real TCP exit over separate
fast/bulk OS TCP carriers with the new framing and Noise. New scenarios execute
authenticated OPEN rather than manually adding streams, including 20 cold TLS
connections, independent policy cancellation and denied targets. Existing manual
stream scenarios still isolate their individual queue and accounting contracts.

An additional one-way component scenario now transfers 1.5MiB across three
streams through actual TLS/IMAP APPEND and UID FETCH, exact wallet admission,
durable server receipt and application byte comparison. It uses three physical
bodies containing 24 logical DATA records; it deliberately isolates bulk service
with whole-record body boundaries. Separate tests exercise fast-work protection
and a closed stream within a body. Lost-reply and canceled-unstarted whole-body
tests exercise exact scoped storage proof and single-spend behavior. These are
component integrity tests, not calibrated network/rate measurements or a duplex
server-origin implementation. See [bulk continuation evidence](experiments/session-v2/BULK_CHECK_RESULTS.json).

A newer duplex component scenario connects both publication/receive halves over
four actual TLS/IMAP connections: one fixed fast and bulk executor per endpoint,
one account, separate DATA/protocol journals and wallets, three logical streams
with short demand, bulk records and nonaligned tails. Both endpoints FETCH all
messages, including self-writes. Own bodies are correlated/accounted independently;
they do not count as peer DATA, bandwidth or refund evidence. All application
bytes are compared, authenticated peer receipts drain logical retention, and
physical wallet/journal debt stays charged. No socket replacement occurs in the
healthy component scenario. See [server/duplex continuation evidence](experiments/session-v2/SERVER_CHECK_RESULTS.json).

This fixture manually establishes streams and enumerates bounded mailbox state.
Its classification/correlation code is test-only, not a production inventory
worker. It is not the 10/15-account primary topology, calibrated provider service,
an application TLS/HTTP sequence over IMAP, or an Android speed measurement.
The server adapter currently durably reserves a one-body grant, commits body
intent/close, then records APPEND outcome; trusted inventory adds observation
work. These real persistence costs must be measured and included in the complete
controller/provider fixture. Neither zero-cost journal service nor an assumed
100Mbps rate follows from these component results.

The newer cold-application IMAP scenario joins OPEN, a real TCP exit, real
application TLS/HTTP, both quota authorities, persistent journals/wallets,
TLS/IMAP APPEND/FETCH and the scoped Inbox. Four new HTTP connections echo both
short and nonaligned bodies larger than the initial receive window. Four fixed
IMAP sockets remain alive; no manual stream creation or synthetic application
response bypasses the pipeline. Noise/grants are still pre-established, and a
single test pump coordinates both endpoints without calibrated provider service.
Server origin classification/correlation now uses the scoped implementation and
original persisted attempts; client own-body classification is still fixture-owned.
This is byte
integrity, not a 250ms/100Mbps result. Five repetitions cover 20 such IMAP-backed
HTTP requests, in addition to 100 cold TLS requests on the raw TCP carrier fixture.
See [OPEN/inbox evidence](experiments/session-v2/OPEN_INBOX_CHECK_RESULTS.json).
Later [journal correlation evidence](experiments/session-v2/CORRELATION_CHECK_RESULTS.json)
records the production server decoder, restart and lost-commit checks.

Earlier versions of this component fixture used a zero-I/O in-memory carrier
loop. One mixed stress cohort timed out. Isolated repetitions passed; an actual
TCP fixture was then substituted. A later diagnostic timeout showed bytes still
arriving, a full receive buffer, and expiration of an arbitrary five-second
**total** deadline on a 2–3 MiB byte-integrity transfer. That fixture now enforces
five seconds **without application-byte progress**. The failed runs are retained
in the continuation evidence. This is not a transport throughput improvement.

The release contract is unchanged: 250ms light maximum, 2000ms loaded maximum,
100Mbps useful sustained traffic, fixed 12s/60s measurements, ramp/tail/stall gates,
legacy OPEN/TLS/HTTP timeouts, zero unexpected TLS failures and zero load-induced
carrier replacements. Component integrity tests cannot satisfy those gates.
The source-test host has a two-CPU quota; it is not the eight-core server profile.

## Still required before release

* Authenticated transport bootstrap, READY/roster negotiation and concrete exit
  authorization policy; wire/control exchange for grants, usage, retirement and
  recovery. OPEN framing/dialing exists, but those authorities are pre-established
  in the current integration fixture.
* Per-carrier measured-service batch selection and its demand/supply coordinator;
  the new concrete-grant aggregation path is not yet throughput-qualified.
* Active carrier selection, controller/receiver-window integration and complete
  status/NACK recovery for both publication/receive halves.
* Production client origin/ledger classification, IDLE/DONE or a calibrated
  alternative, bounded proof-based
  reclamation and migration/restart reconciliation using original stores and
  retained debt. The bounded serialized Inbox is not yet a complete concurrent
  mailbox service; control progress under full DATA pending queues must be
  integrated and qualified before activation.
* Independent provider/client/server processes with calibrated network, command,
  CPU and disk service; all required socket/account/state/workload/seed cases.
* Real native TUN/TCP integration, Android validation, 300 cold TLS attempts per
  primary case, 20 consecutive complete Speedtests, 60s SINGLE/MULTI/duplex checks
  and soak. Component TLS repetitions are not these primary attempts.
* Complete quota/crypto/recovery negative matrix and every primary release gate.

No current result proves sustained 100Mbps on Rambler or <=250ms idle application
RTT. Neither target is established for this implementation. The prior 200+Mbps
simulator peak is not evidence for it. Android identity remains `org.itom.client`;
signing material and installed wallet history have not been changed.
