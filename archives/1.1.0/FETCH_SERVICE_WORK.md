# Shared FETCH service and bounded bulk reads

This is the retained checkpoint report from before the
[temporary 300ms user decision](TEMPORARY_RTT_POLICY.md). Its historical 250ms
results and original source hashes are unchanged.

This continues `96a1447`. It is an unqualified source checkpoint, not an APK or
server release. Production entry points remain `V2_NOT_READY`. The final strict
light-load run still misses 250ms; sustained 100Mbps is not established.

## Changes retained

Discovery and cleanup previously raced to admit an untagged FETCH after each
completion. Losing admission returned a capacity error and both owners retried
on the same edge. The new internal paths queue for that single parser slot
before taking the wire writer. An already waiting owner gets a turn before a
new polling turn. Public fail-fast command APIs remain unchanged. No second
parser, selected socket, pending tag or response allowance is introduced.

The actual operation owns its turn until its tag completes or the executor
fails. Canceling a command waiter cannot release it early. Canceling queued
admission removes that waiter; explicit carrier shutdown also unblocks queued
owners. STORE can still progress independently while a FETCH tag is held.
Diagnostics expose the number of queued internal FETCH callers.

Deterministic tests use actual selected TLS/IMAP, an original authenticated
SERVER body, a missing client MAC and withheld tags. They require maintenance
to progress after discovery, and discovery after maintenance, while keeping
physical debt held. Separate cases exercise canceled command waits, canceled
queued admission and explicit owner shutdown. In a matched command-service
stress comparison, competitive admission passes three of six cold TLS/HTTP
runs; queued admission passes six of six with the same application deadlines.
This fixes a service-order risk, not every source of field latency or timeout.

The legacy canceled-restart check used an old capacity-retry counter as its
waiting condition. Its initial failure is retained. It now observes an actual
queued owner, the still-active original FETCH, exactly one issued inventory and
the same healthy carrier; its original restart/cancellation/NOOP checks remain.
No application deadline or qualification threshold changed.

Unsuccessful receipt/absence probes now back off exponentially within the
original 1–25ms probe bounds. Successful marking/reclamation resets the delay;
already-ready work continues immediately and a new candidate can wake the
timer. A retained, unreceipted body cannot justify a continuous FLAGS loop or a
refund. Repeated quiet checks record 9–11 actual receipt probes in 200ms while
retaining all original physical debt. This is maintenance traffic, not an RTT
or useful-delivery measurement.

Bulk discovery can ask for RFC822.SIZE alongside actual UID inventory, then
fetch two already-known UIDs in one exact-set command when they fit. Each
literal and their total still face the original parser limits, with conservative
header/trailer space reserved before grouping. Missing/unfittable size hints,
limited delivery space and startup handoff boundaries retain single-body reads.
No fill timer, UID range expansion, metadata-based authentication or accounting
shortcut is allowed. Every complete BODY independently passes the existing
scope, quota, replay, admission and delivery checks. Raced-away UIDs retain the
original second-pass behavior; an absent BODY is never financial proof.

The parser can now use more of its existing 4MiB response allowance for one
bulk command, instead of one literal alone. At most two complete raw bodies
are retained for that turn; configured pending-delivery bounds remain separate.
This is not a claim that peak receive allocation is identical to single reads.
Known UID, literal, response, message, folder, grant and session bounds do not grow.

Negative overlays confirm that competitive admission fails the held-tag turn
check, the old maintenance loop makes 113 probes in the quiet 200ms interval,
and single-body service fails the paired-read command-count check. Initial test
expectation failures and all failed intermediate functional cohorts are retained.

An isolated sparse-inventory check on an imposed 80ms TCP path reduces eight
BODY commands to four; the three final repeats average 736ms versus 412ms. A second
check first commits original SERVER attempts and actually APPENDs eight encrypted
publications over TLS. Later discovery, decryption and consumption of their
2MiB application payload averages 849ms with single reads and 633ms with paired
reads. It verifies replay retention and unchanged original physical debt.
**Those measurements exclude publication/startup time and are not sustained
tunnel bandwidth or a speedtest result.** All endpoints share the source host.

## Rejected activation and remaining failures

Paired reads on the fast path were rejected. In isolated application cohorts,
single reads miss 250ms in 17/64 warm exchanges (224.531ms mean, 442.144ms max);
paired reads miss in 27/64 (235.180ms mean, 399.965ms max). Both fail the unchanged
gate. Another early paired run overlapped race-build work and is retained as
confounded evidence, not used as the isolated comparison. The runtime assembler
therefore enables pairing only on bulk carriers; fast reads remain single.

The final candidate, including queued turns and maintenance backoff, completes
all 64 warm exchanges but misses 250ms in 19, with mean 227.172ms and maximum
336.092ms. This does not establish an overall light-load RTT improvement. The
older HTTP deadline failures, long journal update, command-service stress
failures and all intermediate failed checks remain retained. Functional source
checks and isolated command-count improvements cannot qualify a release.

The final ordinary and race core suites each pass 233 top-level test groups;
the native race suite passes ten, and both module vet checks pass. These are
source checks, not hosted CI results or a replacement for the failed light gate.

Receiver-window/controller integration, durable authority turnover, production
lifecycle/recovery/native activation, independent-host/provider measurements,
and the original full-duration sustained-rate, ramp, rolling-window, stall,
TLS and reconnect gates remain outstanding. Stable 1.0.0/RC2, original
quota/journal/proof rules, Android package/signing identity and the completed
DNS race/session fallback are unchanged.

Raw checks, overlays, source hashes and final source-check outcomes are listed
in [FETCH_SERVICE_CHECK_RESULTS.json](experiments/session-v2/FETCH_SERVICE_CHECK_RESULTS.json).
