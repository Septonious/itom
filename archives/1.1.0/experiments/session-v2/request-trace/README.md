# Request-trace evidence

The parent `REQUEST_TRACE_CHECK_RESULTS.json` lists checks, hashes, source
replacements and qualification failures. These are source-host observations,
not a production APK/server or a real Rambler benchmark.

Final functional checks pass 224 core race groups, 10 native race groups, both
module vet checks and six tagged preparation group executions. Every earlier
cold/header/body failure remains retained. The extended light gates are failed.
In particular, the propagation run is incomplete: seven measured warm successes
precede an HTTP header timeout. Its partial successful timings must never be
used as a qualifying mean or as evidence for all 64 requested samples.

`request-trace-light-gate.jsonl` records serial command-service stress.
`request-trace-propagation-parallel-gate.jsonl` records asynchronous TCP/TLS byte
propagation with bounded parallel selection of the listed fixture lanes.
The relay and encrypted NOOP sanity checks report configured values separately
from observed values. All endpoints/provider/relay execute on the shared source
host. These results do not satisfy independent-process, hardware or provider
qualification, sustained rate, 300 cold TLS attempts,20 full Speedtests or soak.

The earlier trace/profile cohorts predate some fixes and, where named diagnostics,
asserted byte integrity while separately retaining latency misses. New explicit
qualification tests use the unchanged shared 250ms check and fail those misses.
No historical success or rename erases a failed sample or incomplete attempt.

The initial relay half-close error and initial maintenance caller selector error
are retained alongside their corrections. Expected-negative overlays distinguish
avoidable discovery turns, unsafe beyond-prefix sequence assumptions, cache
exhaustion without inventory and shared receive/maintenance locking.

To reconstruct a current negative overlay, resolve the parent manifest's source
keys from the repository root, resolve replacement files from this experiments
folder and write an absolute Go `Replace` mapping. Pass it with `go test -overlay`.
Use `previous-poll-compatible` with `TestExactCleanupSequenceChanges`,
`unsafe-tail-rebase` with `TestAmbiguousTailDeletion`, `cache-stop` with
`TestCleanupPrediction`, and `shared-receive-maintenance-lock` with
`TestQueuedPeerReceiptAndStats`. Their failures are expected; none count toward a
passing performance score. Compiled profiling test executables are not included.
