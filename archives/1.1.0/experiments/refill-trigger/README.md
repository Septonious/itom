# Fenced-grant proof-service trigger

A selected1100ms candidate repeats strong throughput and short-request means but one upload-only invocation has an empty1s delivery window. Test a bounded service request when authenticated StorageRelease fences a usable bulk lease and normal offer cannot restore the inbound bulk window.

Compare triggering only when no bulk lease remains (`empty`) with triggering below the normal two-lease window (`low`). This applies only to the model runtime and historical-debt-reduced profiles. A control-starved offer retains local-control cleanup priority. Existing per-lane worker limits, FIFO admission, local/remote arbitration and exact physical proof/refund rules are unchanged. No reconnect or timer manufactures a grant, delivery acknowledgement, deletion proof or free capacity.

This is isolated experimental scheduling. Existing periodic timing remains the fallback. All original qualification gates and additional directional no-empty-window checks remain in force; prior failed and partial rounds are retained.

First complete measured cohorts: `empty` passed14/20 versus RC213/20; RC2 has one cold-start timeout that suppresses two planned rounds, while the candidate completes all20. `low` passed19/20 versus RC217/20. Candidate one-way uploads pass both invocations with no empty1s windows:157.6Mbps (`empty`) or161.7Mbps (`low`) versus RC276.7/77.1Mbps; download-only capacity remains within about0.5% of matched RC2. All eight candidate4MiB bulk-to-quiet invocations pass; three RC2 changing-path invocations time out.

Both accelerated-period variants still show retained duplex download losses, and `empty` misses the numerical17/20 goal. They remain unpromoted; [complete results](results.json) include every planned failure and partial/missing RC2 round. The [original-periodic-cadence comparison](../refill-baseline-cadence/README.md) now isolates the useful refill trigger from unconditional periodic acceleration.

An additional real-journal race regression verifies that64 held control records retain local-control proof priority. It also verifies unchanged historical grants/debt, the full bulk-window guard and explicit legacy behavior; the local race run passes in1.203s. No test or runtime shortcut releases quota.
