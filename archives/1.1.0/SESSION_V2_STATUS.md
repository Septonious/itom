# Replacement transport implementation status

This branch is an **unqualified implementation in progress**, not a runnable RC
or achievement of the 250ms/100Mbps targets. Production client/server startup
still returns `V2_NOT_READY`; packaging is disabled. No APK or deployable server
binary is published from this branch. This is one structural replacement, not a
series of intermediate timing releases.

Preparation of a working field-test candidate is tracked in
[the release preparation checklist](FIELD_RELEASE_PREPARATION.md). The new
`transport.Runtime` now owns integrated session/carrier/exit service, but has not
replaced the production API stubs or completed authenticated initialization.

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
* Client mailbox decoding now pins negotiated store/run, account/user/session,
  folder/incarnation and ledger, and ignores authenticated own-origin echoes
  without decrypting them or reconciling wallet history. Client/server Inbox
  factories bind these routes to the actual selected executor before FETCH.
  MIME/physical authentication is performed once per body before scoped peer
  record decryption or own journal observation, avoiding duplicate decode work.
* Scoped inbox admission now keeps separate bounded DATA/protocol pending slots.
  An authenticated later receipt can pass a full DATA delivery queue. DATA which
  cannot be retained defers after physical scope/MAC validation but before peer
  AEAD/replay consumption. Cached immutable-UID classification avoids repeated
  BODY reads while blocked; bounded probes and explicit More work prevent an
  entire backlog from monopolizing one sweep. These memory/work bounds do not
  borrow protocol quota or change physical envelope ceilings.
* Capacity-free protocol updates can now pass a full protocol delivery queue.
  A pure pre-replay admission check permits receipts and resets of existing
  streams, while deferring OPEN and unknown early RESET which may need memory.
  It never allocates extra queue slots; original crypto byte/failure limits still
  apply even to rejected admission. A deferred OPEN retains replay eligibility
  and its original physical debt. Bounded probe continuations retain examined
  UIDs and switch to prefix order on dependency gaps instead of repeating the
  newest unresolved ordinals indefinitely.
* The fast inbox can read a predicted single next UID after a complete inventory,
  removing the separate enumeration command for a contiguous new message. It
  never advances a missing UID silently: an empty/refused read, sequence gap,
  EXPUNGE/count invalidation or deferred delivery requests full inventory. Actual
  UID scope/proof, response ceilings and replay/accounting stay identical through
  one shared delivery path. Sequence information is a discovery hint only.
* Each selected executor now has one scoped publication worker, independent of
  inbox reads. Ready fast ledgers alternate after the initial protocol turn;
  DATA/protocol allowances remain distinct. Permission exhaustion parks that
  ledger until a real grant/reclamation/recovery notification, without another
  spend or socket closure. A worker retains at most one exact prepared body;
  uncertainty stops writes for explicit recovery rather than blind re-APPEND.
  Physical prepare/publish timing and accepted MIME bytes are exposed separately
  and are never useful-delivery bandwidth or application-RTT samples.
* A verified bounded BODY now applies in one session turn, preventing its
  feedback from being published between records in that same body. Beyond
  initial light traffic, small app reads renew standalone credit in window
  quanta, or immediately at the peer's dispatched edge; DATA still piggybacks
  the latest credit. No collection sleep, quota increase or unbacked credit is
  introduced.
* Each selected executor can now have one scoped receive worker, independent
  of its publication worker. It discovers actual UIDs even if notifications
  are absent, drains ready work immediately and adjusts its bounded probe delay
  from measured idle command service. Cancellation leaves an issued FETCH to
  drain under the original progress timeout; restarting the same worker waits
  for that command rather than replacing the socket. Repeated unchanged EXISTS
  and informational status replies no longer create self-poll notifications.
  This polling alternative is not yet calibrated.
* Verified CAPABILITY negotiation now enables bounded IDLE notification waits
  for explicitly configured quiet bulk/standby readers. Active fast readers keep
  their independent probe policy. An IDLE owner admits no pending tags or queued
  writers, and holds the writer through continuation, DONE and tagged completion.
  An arriving command requests immediate DONE even before the continuation;
  cancellation drains framing under the original progress limit. A complete IDLE
  refusal disables only that executor's notification mode and keeps UID polling
  on the same selected socket. IDLE response bytes retain the original ceiling.
  Missing notifications trigger an actual UID audit, never assumed delivery or
  deletion. Default quiet-service audit is 250ms; this is not a light-RTT claim
  or permission to enable IDLE on the active fast path without qualification.
* Transient pending-command capacity now has a distinct internal error. Inbox
  inventory/body bounds and publication literal limits stop for explicit handling
  instead of becoming endless command-capacity retries. An unstarted publication
  retains its exact body and original charge; a genuinely full command queue
  resumes that body after a tag drains without another spend.
* Isolated versioned receive/send-window state machines now cover conservative
  growth, sender-fenced credit return, delayed old grants, duplicate controls and
  DATA which overtakes a fence. A consumption-demand model requests prompt bounded
  growth without treating sparse lifetime traffic as bulk. These components are
  NOT yet connected to Session/wire credit; they do not enable automatic growth.
  See the [credit integration contract](WINDOW_CREDIT_DESIGN.md).
* Every publisher/receiver route of one logical Session now pins the same exact
  replacement Noise epoch and endpoint role across all accounts, planes and
  ledgers. Independently established keys cannot be substituted even with equal
  public binding labels; legacy epochs reject at cipher construction. OPEN role
  configuration and exit binding are atomic, so a rejected constructor cannot
  claim the session's role. Rejected routes do not spend/clear grants or mutate
  queues. Coordinated rekey still requires an explicit owner transition; binding
  is a prerequisite for scoped window integration, not automatic window growth.
* A single Runtime now validates actual fast/bulk executors, matching ledger
  routes, required protocol service and explicit socket/account budgets before
  atomically claiming the Session and all actors. Already running or unlisted
  workers cannot compete with runtime ownership. The TCP exit uses the same
  owner. No sockets, quotas, grants or Noise keys are created by assembly.
* Runtime supervision preserves receive service after a write failure and stops
  new writing only on a failed reader's carrier. Other carriers continue; exact
  charged publications remain reachable for explicit recovery. Cancellation
  joins issued service; final Close owns session/socket shutdown and borrows
  rather than closes or erases wallets/journals. Startup snapshots report actual
  actor starts and service failure stages, never a fabricated peer READY.
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
fixture read pump plus four independent scoped publication workers coordinate
the endpoints without calibrated provider service.
Both endpoints now use scoped inbox constructors and origin/ledger decoders;
server correlation uses original persisted attempts. This is byte
integrity, not a 250ms/100Mbps result. Five repetitions cover 20 such IMAP-backed
HTTP requests, in addition to 100 cold TLS requests on the raw TCP carrier fixture.
See [OPEN/inbox evidence](experiments/session-v2/OPEN_INBOX_CHECK_RESULTS.json).
Later [journal correlation evidence](experiments/session-v2/CORRELATION_CHECK_RESULTS.json)
records the production server decoder, restart and lost-commit checks.
The [client inbox continuation](experiments/session-v2/CLIENT_INBOX_CHECK_RESULTS.json)
records removal of the last fixture-owned classifier from this application path.
The [inbox pressure regression](experiments/session-v2/INBOX_PRESSURE_CHECK_RESULTS.json)
retains a failing pre-fix queue-pressure run and the passing accounted receipt
path. Queue pressure is injected to isolate admission from receiver-credit
invariants; this is not a field performance reproduction or rate qualification.
The [protocol progress continuation](experiments/session-v2/PROTOCOL_PROGRESS_CHECK_RESULTS.json)
retains failing RESET/dependency reproducers and checks deferred OPEN replay,
physical debt and bounded continuation progress after the fixes.
The [direct read continuation](experiments/session-v2/POLL_CHECK_RESULTS.json)
checks the one-command contiguous read, UID holes, late lower UID discovery and
sequence movement. The cold IMAP application fixture now uses this path on fast
and full inventory on bulk. No command-count saving is counted as measured
application RTT or sustained bandwidth.
The [publication-worker continuation](experiments/session-v2/WORKER_CHECK_RESULTS.json)
checks an independently completing FETCH while APPEND's tag is withheld,
permission wakeups, uncertain-body retention, separate ledger service and the
parallel cold-application IMAP path. The initial repeated run exceeded the
unchanged 64-message fixture bound after feedback amplification; its failure is
retained. Coalesced feedback/body application passes that same bound. These are
integrity/service tests, not independent multi-account performance qualification.
The [window-state continuation](experiments/session-v2/WINDOW_CHECK_RESULTS.json)
checks memory backing across 100 randomized control schedules and the short-work
provisional grant. Its synthetic consumption timestamps and state transitions
are not measured network RTT, throughput or ramp qualification.
It also retains cold HTTP header-deadline misses from the initial full/repeated
cohorts and one of five initial independent-reader repetitions. The fixture now
uses four independent inbox readers on the same four selected sockets and a
broadcast fixture-only availability hint; bounded per-flow and journal timing
diagnostics accompany failures. The subsequent 28 repetitions (112 HTTP requests)
and full source race checks passed the unchanged bounds. The earlier intermittent
deadline misses remain unexplained; these passes are not a real-world startup fix.
The [independent receive-service checks](experiments/session-v2/INBOX_WORKER_CHECK_RESULTS.json)
remove the fixture-only mailbox hint from the cold HTTP path. Added command-service
variation uses 10–50ms client and 20–40ms server scripted delays, with unchanged
OPEN/TLS/HTTP deadlines. These delays are not equivalent to calibrated network
RTTs, provider parallelism or bandwidth. A default parallel-package race cohort
missed the 1500ms OPEN deadline in this new case; that failure is retained and
remains a qualification concern even when isolated repetitions pass. Timing
profiles expose source-check overhead without authorizing a timeout increase.
The [IDLE service checks](experiments/session-v2/IDLE_CHECK_RESULTS.json) cover
writer preemption around the continuation, strict DONE/tag ordering, cancellation,
initial/final refusal, missing notifications, pending-tag exclusion and response
flood bounds. The cold HTTP fixture now also tests quiet bulk IDLE with actual
unsolicited EXISTS on the selected connections; no test wake channel enters the
receive worker. Original polling cases remain. The final default-parallel full
race cohort passes, but does not prove the earlier OPEN miss was eliminated or
establish application RTT/bandwidth targets.
The [session-binding checks](experiments/session-v2/BINDING_CHECK_RESULTS.json)
run the same four regression groups against archived `8515029` and corrected
source. The archived implementation accepts invalid epoch/role attachments;
the corrected source rejects them before any spend, preserving original wallet
and journal exposure and allowing a wrapper of the same original epoch. Full
default-parallel core race checks pass with these guards. This is scope/counter
ownership validation, not a measured RTT or bandwidth improvement.
The [runtime integration checks](experiments/session-v2/RUNTIME_CHECK_RESULTS.json)
replace the cold application's fixture supervisor with paired production runtime
owners. Resource/ownership rejection, cancellation, healthy post-run sockets,
partial service failure and retained exact body/charge are exercised. The first
three-repeat cold cohort retains TLS/startup deadline misses; one failure's
journal metrics show a commit of about 1.3 seconds. Subsequent full source checks
pass unchanged deadlines, but no timing failure is declared eliminated. Source
CI now preserves machine-readable passing/failing logs and environment metadata.

Earlier versions of this component fixture used a zero-I/O in-memory carrier
loop. One mixed stress cohort timed out. Isolated repetitions passed; an actual
TCP fixture was then substituted. A later diagnostic timeout showed bytes still
arriving, a full receive buffer, and expiration of an arbitrary five-second
**total** deadline on a 2–3 MiB byte-integrity transfer. That fixture now enforces
five seconds **without application-byte progress**. The failed runs are retained
in the continuation evidence. This is not a transport throughput improvement.

A later full-suite run also exposed an absolute four-second lifetime in the
shared TLS/IMAP byte-integrity fixture: the duplex test lost its fixture socket
at 4.75 seconds under the two-CPU source-check host. That fixture now enforces
four seconds without read/write progress, retaining its body/count bounds. The
failed full-suite output is preserved in the inbox-pressure evidence. Production
progress timeout, 1500ms OPEN/TLS limits and all timed qualification gates are
unchanged; this correction is not a measured transport speed improvement.

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
* Calibration and multi-account integration of IDLE/DONE and active probing,
  bounded proof-based reclamation and migration/restart reconciliation using original stores and
  retained debt. The scoped receive/publication workers are not yet a complete
  multi-account mailbox coordinator. DATA pressure no longer consumes protocol delivery slots;
  capacity-free protocol progress now bypasses exhausted protocol slots too.
  Multi-account fairness/liveness and complete memory/service behavior still
  need integration and qualification with the coordinator and recovery paths.
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
