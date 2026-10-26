# Bounded BODY/FLAGS overlap on one selected connection

The receive and maintenance owners can now issue one singleton BODY read and one
FLAGS query concurrently, for disjoint UIDs on the same selected TLS connection.
They retain independent tagged completions and distinct response destinations.
This removes a serialization round trip from eligible two-query service; it
does not add a socket, second parser, permission or replay shortcut.

Inventory, independent absence proof, overlapping UID sets, grouped bulk reads,
narrow response budgets and public fail-fast FETCH calls retain exclusive
admission. FIFO admission precedes the wire writer: a queued exclusive query
cannot be bypassed by later compatible probes. Cancellation removes unstarted
requests; an issued tag owns its slot until completion or original timeout.
Both response streams share the original aggregate response-byte bound. A
singleton literal and bounded FLAGS framing must fit before overlap is allowed;
actual parsing still enforces the aggregate cap. Failure clears parser targets
before further buffered frames can mutate completed operations.

Physical debt, full BODY authentication, client-MAC receipt, exact EXPUNGE and
separate same-incarnation absence proof remain unchanged. No quota or receiver
window grows, and no timeout changes. The light maximum remains 300ms, preferred
250ms; loaded RTT and sustained bandwidth requirements remain 2000ms/100Mbps.

Eight new protocol groups and the updated fairness/cancellation scenarios
exercise independent and reversed completion order, overlapping UIDs, exclusive
proof admission, canceled queue heads, missing tags, wrong-UID BODY replies,
aggregate overflow, grouped bulk and narrow budgets. Ten targeted groups repeated
three times pass under the race detector. The parent source fails the new
independent-completion regression as expected. Final core race checks pass all
249 groups; native race checks pass ten and both modules pass vet.

An intermediate transport-only race run passes 208/209 groups. Its plain selected
TLS NOOP calibration records 503.779ms on the configured 10–50ms wire profile.
That case does not exercise FETCH overlap. Subsequent standalone calibration
repeats complete sixty probes in each variant, all below 53ms. The earlier failed
run remains failed; those repeats neither erase it nor establish a sole cause.

## Measured service and application results

The same one-socket TLS parser-service component, with a constant 50ms opaque TCP
route and no provider command sleeps, averages 102.201ms before and 51.382ms
after for twenty BODY/FLAGS pairs. New counters observe all twenty overlapping
pairs. This is parser service, **not application RTT or tunnel bandwidth**; it
does not execute Noise, journal or flow-control service.

The full accounted ten-account TLS/HTTP scenarios retain every sample:

| Scenario | Parent | New implementation |
| --- | --- | --- |
| Warm fluctuating paths | 64 complete; mean 189.397ms; 1 over 300ms | 128 complete; mean 184.166ms; 2 over 300ms |
| Warm maximum | 309.631ms | 329.496ms |
| Fixed client/server 50/40ms | 16 complete; mean 307.619ms; 5 misses | 16 complete; mean 293.190ms; 4 misses |
| Fixed upper-range maximum | 787.060ms | 612.321ms |
| First exchange after 1.13/5.07/30.19s silence | 3 complete; maximum 641.267ms; 1 miss | 3 complete; maximum 268.635ms; no misses |

Both warm cohorts and the upper-range cohort still FAIL the same maximum.
The new quiet cohort passes, but three samples cannot establish field stability.
The worse warm maximum is not filtered out. The two warm loops observe 382/387
overlapping pairs including priming and background maintenance; those counters
are not useful-delivery or bandwidth samples. Unequal cohort sizes and this
shared source host limit the comparison; no universal RTT improvement is claimed.

Production initialization/native activation, authority turnover, dynamic windows
and controller integration remain incomplete. No sustained-rate qualification or
working APK/server release is produced. See the [results and hashes](experiments/session-v2/FETCH_OVERLAP_CHECK_RESULTS.json)
and [raw evidence/replay instructions](experiments/session-v2/fetch-overlap/README.md).
