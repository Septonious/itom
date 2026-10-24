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
* Bulk physical-body aggregation and service-sized batch selection: the current
  client publication path carries one logical record per BODY and is not a
  throughput-qualified batching path.
* The server-origin publication/client receive half, active carrier selection,
  controller/receiver-window integration and complete status/NACK recovery.
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
