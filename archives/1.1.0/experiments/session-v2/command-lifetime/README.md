# Command lifetime evidence

Parent source: ac73392e6a71298901ec9e7e236636bba884b5dd.

The only production change replaces one sleeping command-watchdog goroutine
with a time.AfterFunc timer, stopped on every tag/failure/close terminal path.
A fired callback still checks completion under the same executor mutex.
The default 30-second progress limit, detached waiter rules, quota, DNS and
all application deadlines are unchanged.

`before-warm.jsonl` has CPU profiling enabled; `before-traced-warm.jsonl`
has execution tracing enabled. They are diagnostic reproductions, not matched
unprofiled A/B RTT trials. The first fails after 20 warm completions; the second
completes 64 but has eight violations of 300ms. Profile summaries are retained;
large binary profiles remain in the execution workspace, not the repository.

`watchdog-race.jsonl`: five targeted top-level groups repeated three times,
all 15 pass under the race detector. Tests exercise real selected TLS sockets,
missing provider tags after application cancellation, retained unknown outcomes,
tagged refusal, subsequent healthy reuse, and existing cancellation/FETCH/IDLE
framing checks. Short test progress limits do not alter the production default.

Before/after benchmark runs use the same new bookkeeping benchmark. The before
variant overlays exactly the two parent implementation files. Three 500ms runs:
old 2046–2442 ns/op, 672–677 B/op, nine allocations; new 1060–1142 ns/op,
536 B/op, seven allocations. This is only command bookkeeping, not throughput.

The new warm cohort completes all 64 but nine exceed 300ms (maximum 829.444ms).
The quiet cohort completes all three but one exceeds 300ms (maximum 386.312ms).
Both qualification gates FAIL. No claim of field RTT improvement, sustained
100Mbps, resolved HTTP instability, or release readiness follows from these data.

The full default source race suite passes 237 top-level groups, native race checks
pass ten, and both module vet commands exit zero. Their raw output is retained.
Those source checks do not override either failed application qualification gate.

`original-unprofiled-warm.jsonl` is a further parent-source overlay run without
profiling: all 64 complete, with 12 misses, mean 239.923ms and maximum 706.076ms.
The new implementation's cohort has fewer misses but a higher mean and maximum.
This mixed, shared-host observation cannot establish an application latency win
or eliminated HTTP instability. Every cohort remains visible.

For the parent implementation comparison, write `git show
ac73392e6a71298901ec9e7e236636bba884b5dd:archives/1.1.0/source/core/transport/imap.go`
and the corresponding `imap_diagnostics.go` to scratch files and map the two
absolute current source paths to them in a Go overlay JSON `Replace` object.
Run the same current bookkeeping benchmark with `go test -overlay=PATH
./transport -run '^$' -bench '^BenchmarkCompletedIMAPCommandLifetime$'
-benchtime=500ms -count=3`; repeat without the overlay for the new implementation.
The current new tests/benchmark stay present in both variants; no source deadline
or qualification threshold is substituted by the overlay.
