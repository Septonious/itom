# Paired inbound/outbound reclamation experiment

The headroom-scaled remote cleanup candidate repeatedly improved qualification counts, but retained simultaneous-duplex download regressed. Additional one-way comparisons preserved download capacity. Test whether the earlier inbound cleanup makes the existing outbound cleanup eligibility too late under simultaneous traffic.

Keep the 1100 ms remote floor and compare local proof eligibility at one eighth and one sixteenth of the recovered outbound byte window, versus the current quarter. The existing 64 KiB floor, 1 MiB ceiling, FIFO service, per-lane maintenance worker and exact physical deletion proofs remain unchanged. Fresh profiles keep the original threshold. Eligibility cannot refund or grant capacity.

Run unchanged RC2 and each candidate in full AB/BA cohorts with all original qualification gates, plus separate one-way workloads. These experiments do not change active DEV3 or final 1.0.0 source. Do not accept a pass-count improvement that hides a serious directional loss.
