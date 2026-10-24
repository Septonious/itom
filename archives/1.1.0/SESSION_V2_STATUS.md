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

## Verification boundary

All old owned Go tests were removed after the first implementation, as requested.
Their manifests and immutable Git history remain; third-party tests and release
archives were untouched. Fresh scenario and invariant tests are independent.

Current application/TCP integration runs real TLS/HTTP and a real TCP exit over
separate fast/bulk OS TCP carriers with the new framing and Noise. It manually
establishes the logical streams. It does **not** execute transport OPEN over IMAP,
provider mailbox polling/reclamation or Android TUN. Quota/IMAP/BODY tests cover
those individual components separately; they do not yet form the full pipeline.

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

* Authenticated transport bootstrap, OPEN/READY/roster negotiation and target
  authorization; wire/control exchange for grants, usage, retirement and recovery.
  The stream adapter currently starts after an externally established OPEN_OK.
* Per-carrier measured-service batch selection and its demand/supply coordinator;
  the new concrete-grant aggregation path is not yet throughput-qualified.
* Active carrier selection, controller/receiver-window integration and complete
  status/NACK recovery for both publication/receive halves.
* Scoped bidirectional mailbox inventory/discovery, origin/ledger classification,
  IDLE/DONE or a calibrated alternative, bounded proof-based reclamation and
  migration/restart reconciliation using original stores and retained debt.
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
