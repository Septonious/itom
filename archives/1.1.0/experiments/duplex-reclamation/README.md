# Paired inbound/outbound reclamation experiment

The headroom-scaled remote cleanup candidate repeatedly improved qualification counts, but retained simultaneous-duplex download regressed. Additional one-way comparisons preserved download capacity. Test whether the earlier inbound cleanup makes the existing outbound cleanup eligibility too late under simultaneous traffic.

Keep the 1100 ms remote floor and compare local proof eligibility at one eighth and one sixteenth of the recovered outbound byte window, versus the current quarter. The existing 64 KiB floor, 1 MiB ceiling, FIFO service, per-lane maintenance worker and exact physical deletion proofs remain unchanged. Fresh profiles keep the original threshold. Eligibility cannot refund or grant capacity.

Run unchanged RC2 and each candidate in full AB/BA cohorts with all original qualification gates, plus separate one-way workloads. These experiments do not change active DEV3 or final 1.0.0 source. Do not accept a pass-count improvement that hides a serious directional loss.

First complete cohort: local8 passed16/20 versus RC216/20, including one partial candidate round ending at52.5s after a scheduled probe timed out. The changing-path RC2 reference on that host also fell below100Mbps in all four rounds, but that does not excuse the candidate timeout. Local16 passed19/20 versus RC218/20 with complete rounds/probes and preserved sockets. Its retained duplex download still fell4–6%, while additional one-way download improved and retained upload stayed above100Mbps. Full individual invocations and the failed/partial round remain in [results](results.json).

[One-way checks](results-directional.json) and [retained idle/light checks](results-interactive.json) are separate from the20-round score. Local16 is undergoing independent confirmation. An observation-only fixture records raw-path timing and aggregate journal/queue counters around short requests; those concurrent totals are not critical-path latency attribution. No source or package has been promoted.

The first full suite rejected the unconditional divisor because an explicit legacy-controller unit test correctly expected the established quarter-window trigger. The follow-up policy uses the new divisor only when the model runtime is enabled; explicit legacy factories retain their original behavior and assertions. Actual model supervisors are requalified after this guard. No qualification threshold or legacy assertion is changed.
