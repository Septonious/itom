# Paired inbound/outbound reclamation experiment

The headroom-scaled remote cleanup candidate repeatedly improved qualification counts, but retained simultaneous-duplex download regressed. Additional one-way comparisons preserved download capacity. Test whether the earlier inbound cleanup makes the existing outbound cleanup eligibility too late under simultaneous traffic.

Keep the 1100 ms remote floor and compare local proof eligibility at one eighth and one sixteenth of the recovered outbound byte window, versus the current quarter. The existing 64 KiB floor, 1 MiB ceiling, FIFO service, per-lane maintenance worker and exact physical deletion proofs remain unchanged. Fresh profiles keep the original threshold. Eligibility cannot refund or grant capacity.

Run unchanged RC2 and each candidate in full AB/BA cohorts with all original qualification gates, plus separate one-way workloads. These experiments do not change active DEV3 or final 1.0.0 source. Do not accept a pass-count improvement that hides a serious directional loss.

First complete cohort: local8 passed16/20 versus RC216/20, including one partial candidate round ending at52.5s after a scheduled probe timed out. The changing-path RC2 reference on that host also fell below100Mbps in all four rounds, but that does not excuse the candidate timeout. Local16 passed19/20 versus RC218/20 with complete rounds/probes and preserved sockets. Its retained duplex download still fell4–6%, while additional one-way download improved and retained upload stayed above100Mbps. Full individual invocations and the failed/partial round remain in [results](results.json).

[One-way checks](results-directional.json) and [retained idle/light checks](results-interactive.json) are separate from the20-round score. Local16 is undergoing independent confirmation. An observation-only fixture records raw-path timing and aggregate journal/queue counters around short requests; those concurrent totals are not critical-path latency attribution. No source or package has been promoted.

The first full suite rejected the unconditional divisor because an explicit legacy-controller unit test correctly expected the established quarter-window trigger. The follow-up policy uses the new divisor only when the model runtime is enabled; explicit legacy factories retain their original behavior and assertions. Actual model supervisors are requalified after this guard. No qualification threshold or legacy assertion is changed.

The independent model-only local16 confirmation passed19/20 duplex rounds versus RC220/20 in that cohort; the original hypothesis does not establish a consistent qualification-count advantage. All model4MiB bulk-to-quiet cases and worst4G pass, with improved fixed40ms short-request means, but one of the two retained upload-only runs contains an empty1s window. That additional stability failure blocks packaging despite an average102.5Mbps upload. Complete [confirmation results](results-confirmation.json) include every failed round and that empty window.

The [observation report](results-idle-observation.json) preserves1024 paired request timings and raw-path conditions across fresh/retained40ms and changing40–180ms paths. Fixed40ms model maxima are193.8ms fresh and221.2ms retained, versus314.5ms and380.6ms for RC2; changing-path tails remain mixed. Complete before/after store and queue snapshots remain in the linked workflow artifacts. Journal totals are aggregate concurrent work, not critical-path latency; latest wait gauges must not be differenced as cumulative counters.

The next isolated [refill-trigger experiment](../refill-trigger/README.md) requests existing proof service when a fenced bulk lease cannot be replaced. The periodic candidate remains unpromoted.

The guarded model-only candidate passes the complete Go/vet suite and accounting/controller/reader races in [job119509841569](https://github.com/Septonious/itom/actions/runs/39682973026/job/119509841569), including every unchanged legacy assertion. Correctness success does not override the additional directional stability failure.
