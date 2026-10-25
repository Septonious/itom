# Request latency, receive maintenance and byte-level lab links

This continues the per-account source checkpoint `cc0c793`. It is not a working
APK/server release. Production entry points remain `V2_NOT_READY`; the strict
light-load tests still fail and sustained 100Mbps is not established.

## Implemented changes

Receive turns now use a separate mutex from receipt/cleanup state. One receiver
still serializes its complete inventory, BODY accounting, replay and delivery.
Its network wait no longer owns the maintenance-state mutex. Maintenance keeps
its existing single command owner, selected executor, exact UID sets, client MAC
receipts and separate absence proofs. Queuing tokens and reading their counters
use short critical sections. This creates no connection, slot, grant or refund.

The lock profile found about 117 seconds of cumulative waiting attributed to
receive turns across the entire multi-carrier cohort. That is neither one
request's delay nor proof of its only cause. A deterministic regression now
withholds a future BODY tag while an already authenticated peer body's MAC
receipt is queued. Receipt marking and its snapshot must progress without that
tag. Original physical debt must remain held. The corrected shared-lock overlay
fails the 200ms snapshot check; the candidate passes repeated checks. The first
version of the caller used the wrong textual inventory selector, and those
initial caller failures remain in the evidence too.

A bounded history of thirty-two parser sequence changes can retain contiguous
UID prediction across exact EXPUNGE notifications. Each BODY pins its parser
position so an EXPUNGE before or after its literal is interpreted in order.
Rebasing requires complete history and deletes entirely within the previously
observed prefix. A count decrease without exact EXPUNGE, a history gap, or an
ambiguous deletion beyond that prefix forces full inventory. The latter rule is
necessary: a late lower UID can move an old tail before deletion and otherwise
mask the sequence gap. Full inventory must show consistent contiguous sequence
positions before prediction resumes. At the unchanged known-UID ceiling, actual
inventory may prune absent discovery entries; it never refunds their debt.

Tests retain late lower UIDs, ambiguous deletion, overflow and cache pressure.
The preceding rewrite's polling path pays another inventory turn after exact
cleanup; its compatible overlay fails the new contiguous-discovery assertion.
An intentionally unsafe beyond-prefix variant misses the lower-UID safeguard.
These are command-count/correctness results, not sustained-rate qualification.
BODY reads remain single-message and all existing literal/response limits remain.

Journal and client-wallet updates now share one process-local monotonic clock
and a bounded eight-event tracker. Snapshots do not take their database/owner
mutex. They preserve in-progress updates and detached longest-update records;
wallet counters describe runtime updates, excluding open/directory sync/close.
Original synchronous transaction completion, callback order, fsync calls,
failed-closed admission and old writer history are unchanged. Complete update
times include transaction work and scheduling; they are not isolated fsync.
The tests verify observability during a blocked real update without early writer
authority and without changing admission after an unexpected failure.

## Application and link measurement

Request traces now cover response BODY reads as well as headers. Warm HTTP
samples reuse a real TLS connection and retain every 250ms miss. A bounded
sampler records stream state, timer lateness, actor snapshot duration, carrier
service deltas and overlapping journal/wallet updates. Snapshot duration includes
the copy operation and is an upper bound on mutex wait. Profiling never changes
request deadlines or converts a source pass into a latency pass.

The command-service stress fixture deliberately sleeps before each IMAP command,
including DONE completion. It is useful stress coverage, but serial command cost
is not a model of raw client/server RTT with a nearly perfect provider. Those
older failures remain disqualifying evidence rather than being deleted or
reported as solved.

A new test-only relay applies delay and a shared directional byte budget to
opaque TCP/TLS bytes. Commands can pipeline while prior bytes propagate; TLS
verification remains end to end. Both directions use actual OS TCP, bounded
thirty-two-packet queues of at most 16KiB per packet, with bounded reader/writer
buffers. Connections on an endpoint share the directional serialization budget.
A FIFO prevents stream reordering when the configured delay falls. EOF drains
queued bytes and propagates half-close without closing the application's read
side early. The initial relay violated that ownership rule; its half-close test
caught it and the fixed calibration passes.

The link checks verify imposed propagation, serialization, streaming without an
RTT charged per read, half-close and independence from the dial context after
connection establishment. An encrypted selected-IMAP NOOP check records all
observed values alongside the configured 10/50/20/40ms sequence. Shared-host and
TLS overhead can exceed a configured RTT; recorded observations must not be
relabeled as exact path bounds. This user-space relay is not kernel netem or a
real-provider result. A private-namespace netem probe found the qdisc unavailable.

The propagation case uses client RTT 10/50/20/40ms and server 20/40/30ms, changing
by time, with explicit 500Mbps directional budgets and no synthetic provider
command sleep. Every selected socket, including rendezvous/Noise/roster, takes
its endpoint's route. Forty selected executors still serve ten accounts within
explicit twenty/thirty phone/server budgets. Relay TCP sockets model the lab
network and add no authenticated provider sessions. Fixture selection uses eight
bounded workers on only the listed lanes; serial selection under real delay can
leave early fixture sockets idle beyond their original four-second guard. The
three-second dial and four-second guard were not extended. This fixture opener
is not a production lifecycle coordinator.

The first propagation attempt failed during roster construction. Bounded parallel
fixture setup lets the workload run, but that later run still fails its original
HTTP header deadline. Its request trace includes an approximately one-second
journal update overlapping the failed warm request. The test files are on the
source host's tmpfs; that observation alone cannot be blamed on disk or fsync,
and it does not justify disabling persistence. The one-second class of failure
is not resolved. The extended service-stress gate also still exceeds 250ms.

The new propagation check uses the original `qualification.CheckExchanges` and
fails any light sample over 250ms or any HTTP failure. CI records that gate
separately from race-instrumented functional checks, retaining its JSON even on
failure. Passing functional checks cannot make this job or the release ready.
No attempt relaxes the loaded 2000ms, sustained 100Mbps, ramp/tail/rolling/stall,
TLS failure or reconnect thresholds. All old failure cohorts remain available.

Evidence and source hashes are in
[REQUEST_TRACE_CHECK_RESULTS.json](experiments/session-v2/REQUEST_TRACE_CHECK_RESULTS.json).
Original quota/journal/recovery rules, stable 1.0.0/RC2, Android package/signing
identity and the completed DNS resolver race/fallback are unchanged. Production
ownership/full inventory, missing-history recovery, authority policy turnover,
receiver-window/controller integration, native activation and full independent
process/provider qualification remain required before a deployable release.
