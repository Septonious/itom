# Retained-state diagnostic profile

The instrumented RC2+cleanup4 diagnostic encountered an application worker closed pipe at startup after37s. This is an unresolved stream failure, not evidence of a carrier outage. Full tracing on a subsequent run completed all30probes and original/fresh socket checks; its overhead reduced throughput, so it is not qualification evidence. Teardown socket failures were observed only after intentional load shutdown in that trace.

The37s CPU profile sampled14.27CPU seconds; it did not show steady saturation of the two available CPUs. ScanAttempts allocated118.90MB (21.49% of553.34MB total), mainly eagerly reserving256owned metadata rows even on empty/sparse scans. ScanGrants similarly allocated20.27MB. Exact same-transaction bounded pre-counting removes that unused backing allocation without caching, changing JSON/on-disk schema, skipping validation, or changing reservation/proof/commit behavior. All journal tests passed0.140s.

Microbench allocation results: empty49,568→416bytes/op,8rows63,235→15,850,32rows104,196→61,549. These are allocation evidence only. CPU timing of the first comparison is not accepted because local compilation overlapped; repeat it serially before making a CPU-speed claim. This optimization is a separate experiment, not yet selected into the release source.
