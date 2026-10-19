# Diagnostic only: fast reply delay under a shared provider cap

Configuration: complete APPEND clock + unloaded echo + pressure-drain + aligned
reserve prototype. This entire controller bundle failed qualification and is
rejected. Tracing/snapshots were enabled, so these timings diagnose a path and do
not qualify performance. All30probes completed, maximum1374ms; a clean repeat
timed out. Do not select only the trace run.

For logical stream129, offset100, the server emitted a9-byte reply on accounta04.
Its APPEND completed in134.462ms; the client observed the DATA frame1122.298ms
after writer preparation, about988ms after tagged APPEND completion. Preparation
and durable grant work were negligible. Other a04 mail operations also paused
during the interval. This rules out attributing the entire spike to APPEND or
local journal preparation. It does not by itself prove cleanup caused the pause.

Across the surrounding2.1s, socket-counter deltas showed3,284,203BODY-reader bytes
and302,391maintenance-reader bytes. Bulk DATA reception dominated these observed
reads. Cleanup was concurrent, but cannot explain all traffic in this interval.
Full raw synthetic traces remain in workspace scratch; no encrypted packet dumps
are added to the repository.

The production load fixture defaults to one DATA shard. The existing mixed-mail
quiet-reader selector excludes sharded carriers, so the aligned-reserve change
does not exercise that selector in these qualification cases. Its standalone
mixed-mail unit result cannot establish an explanation for this sharded failure.
Do not interpret differing aligned/unmodified throughput rounds as a proven
alignment gain.

The DATA reader currently jumps from1to4BODYs after a successful large read whose
rate exceeds a4Mbps command-safety calculation. This can permit long multi-BODY
bursts while remaining safely below the existing30s command limit. Next isolate
measured1/2/4page growth from two full fast commands, with the same complete BODY,
streaming callback, cursor, physical proof and progress/absolute timeout rules.

A separate experiment adapts only existing FIFO admission for local DATA cleanup.
Independent control reclamation retains its original capacity and fast spares.
Two distinct accounts must corroborate a parallelism increase or decrease; one
paced account cannot reduce the healthy pool. Each account keeps its own SELECT
floor. Empty, unknown, tiny, demand-limited and detached-carrier samples cannot
prove extra parallel capacity. No logical window, quota or DATA writer changes.
