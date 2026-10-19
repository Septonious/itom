# RC11 development

Baseline: exact RC10.1 source build 76d08dc. Qualification thresholds, Android org.itom.client identity, physical profiles, flight limits and wire protocol are preserved.

Implemented:
- Fully validated duplicate ObserveMany and ApplyRemote calls roll back read-only transactions without fsync. Changed/mixed batches still commit atomically; commit failures still poison the store. No observations, deletions, quota releases or permissions are fabricated. Health separately counts Noop transactions.
- Unanswered historical grant reconciliation retries back off 15/30/60/120/240/300 seconds, capped at five minutes. Durable Used/Registered/Released progress resets the cadence; congestion retains its 45-second minimum. Debt and original session bindings remain authoritative.
- Regression tests cover duplicate/forged/mixed proofs, retained unknown debt, lost commit acknowledgement, historical retry progress and repeated 24-stream home-path startup/tails under retained debt.

Verification is in progress. No claim of real-world Speedtest resolution, sustained 100 Mbps or sub-200 ms qualification is made. Downstream service ordering changes remain deferred pending evidence; bulk pacing and recovery bounds are unchanged.
