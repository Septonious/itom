# Replacement transport implementation status

This branch is an **unqualified first implementation**, not a runnable RC or an
achievement of the 250ms/100Mbps targets. Production client/server startup fails
explicitly with `V2_NOT_READY`. There is no fallback to the removed transport.
No APK or deployable server binary is published from this branch.

The predecessor is preserved at `d16796d85b91b1bfe091ad0f24c151ba2f0429a3`.
The removal manifests record paths and SHA-256 hashes. Historical transport
behavior is not an implementation or test oracle. Field captures supply failure
cases and required workload shapes; immutable historical binaries may later be
used only as comparison controls. Cryptographic and accounting contracts remain
mandatory independently of the old scheduling implementation.

Implemented from the new contract:

* `core/transport`: session-owned bounded stream queues and reorder buffers,
  shared memory reservations behind receive credit, fast/bulk dispatch, short
  burst classification, prompt publication without fill sleeps, and a useful
  receiver-service model that does not reduce flight on loaded RTT alone.
* A new verified TLS/LOGIN/CAPABILITY/SELECT bootstrap, with pinned mailbox and
  UIDVALIDITY. One bounded parser owns tagged completions; APPEND can overlap
  UID FETCH. Synchronizing literal barriers and negotiated LITERAL+ are explicit.
  App cancellation detaches a waiter without closing the shared connection.
  Tagged refusal and uncertain publication are different outcomes.
* Independent v2 framing and Noise domain. The existing crypto authority retains
  nonce allocation, replay checking, authenticated scope and key usage limits.
  New sessions cannot use the legacy record API under v2 keys. Own-direction
  loopback cannot become peer data.
* Atomic wallet admission tickets select a concrete grant using its remaining
  encoded bytes, message slots and pending-descriptor capacity. Successful spend
  and ticket consumption share the durable transaction mutex. Uncertain physical
  publications remain charged; unused admission cancellation is not a refund.
* New receiver-trace checks enforce full-phase rate, ramp, tail, exact rolling
  windows, stalls and application RTT maxima. No successful-only or peak-only
  performance score is a release qualification.

After writing the first implementation, all old owned Go tests in active
core/native were removed as requested. Tests in already removed runtime packages
are listed in `REMOVAL_MANIFEST.json`; other owned tests are listed in
`TEST_REPLACEMENT_MANIFEST.json`. Third-party sources/tests and historical release
archives are untouched. Fresh tests cover protocol ordering, verified TLS,
both literal modes, app abort isolation, uncertain outcomes, byte reordering,
half-close, memory/credit backing, quota concurrency, wallet loss, durable debt,
UIDVALIDITY proof rejection, nonce/replay safety and measurement rejection rules.
These do not yet replace the complete integration/security qualification matrix.

Still required in this same structural replacement before any release:

* Session OPEN/READY/roster negotiation, stream/socket adapter, cumulative
  delivery receipts, bounded retirement and reliable handling of admitted work
  across known/unknown publication outcomes.
* Bidirectional mailbox inventory and discovery, fast carrier roster agreement,
  canonical MIME carrier pipeline, server-side journal ticket integration,
  authenticated grant/control exchange, bounded proof-based reclamation, legacy
  store migration and restart recovery under real socket limits. The new executor
  currently implements APPEND/FETCH/NOOP; IDLE/DONE and reclamation are not yet
  implemented. No performance claim assumes those costs are absent.
* Separate provider/client/server processes with independently calibrated paths,
  real disk/CPU costs and complete workload/state/seed manifests. The scripted
  loopback provider tests are protocol tests, not that calibrated laboratory.
* Actual native TUN/TCP exit integration and Android first-run, SINGLE, MULTI,
  300 cold TLS attempts, 20 consecutive sequences, 60s duplex and soak checks.
  The new native test exercises OS TCP half-close only, not the full TUN path.
* Complete quota/crypto/recovery negative matrix and all required primary gates.
  A passing `go test ./...` cannot stand in for these missing integrations.

No source-level unit result proves sustained 100Mbps on Rambler or <=250ms idle
RTT. Neither threshold has been established for this implementation. The prior
200+Mbps simulator peak is not used as evidence for it.
