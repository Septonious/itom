# Bounded IMAP command lifetimes

Every issued IMAP command previously created a goroutine that waited for its
completion or progress timeout. Quiet multi-account polling repeats that setup
for thousands of short commands. A cancellable runtime timer now owns the same
independent progress lifetime; healthy completions need no sleeping watchdog
goroutine. All terminal paths stop the timer under the completion mutex. A
callback already dispatched must acquire that mutex and check the original
completion channel before it can invalidate a carrier.

Application cancellation still only detaches a waiter. A provider that never
returns its tag invalidates the socket once and leaves outstanding publication
outcomes unknown. Neither a timeout nor a canceled callback grants permission,
retries a body, resets wallet history or changes accounting. The default remains
30 seconds. No OPEN/TLS/header/client timeout, DNS policy or acceptance gate grew.

New real-TLS tests retain two missing tags after an application detaches, verify
their independent timeout, and verify that completed/refused commands cannot
later invalidate healthy service. Together with existing FETCH/IDLE/literal
cancellation tests, all 15 targeted repeated race groups pass.

Three isolated bookkeeping benchmarks reduce nine allocations to seven and
2046–2442ns to 1060–1142ns per completed command. They do not measure useful
transport bandwidth or application RTT. CPU/execution profiles of the parent
source reproduce the application failure but do not establish a sole cause.

The new warm application cohort completes 64 exchanges with nine violations
of 300ms and an 829.444ms maximum. The quiet cohort completes three exchanges
with one violation and a 386.312ms maximum. Both gates still fail. This bounded
overhead reduction is not a qualified latency improvement or working release.
Intermittent HTTP stalls, production lifecycle/native activation, authority
turnover and controller/window integration remain unfinished.

Raw failures, benchmark comparisons and profile summaries are retained in
[command-lifetime evidence](experiments/session-v2/command-lifetime/README.md).
