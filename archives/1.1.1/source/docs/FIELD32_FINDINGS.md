# FIELD32: restored RTT/bandwidth governor — work candidate

Current acceptance remains >=100 useful Mbps in EACH direction and every loaded application RTT strictly <1000ms. A controller setting or local synthetic PASS is not field qualification. Packet-loss work and 200Mbps are deferred; do not simulate more than half the accounts failing.

## Restored mechanism

The original gate51 controller is retained unchanged in `docs/experimental/latency_control_gate51*.disabled`. Its concurrency and token-bucket policy is now connected to the shared logical session, before physical DATA admission. Fast DATA, OPEN/Echo, WINDOW/RECEIPT, control and physical cleanup bypass it. Existing 4MiB logical/account/RX bounds, 3MiB stream window, journal bounds, two DATA workers/account and physical proof remain unchanged. The existing S2C 10–150Mbps reader-feedback pacer remains in use.

The historical baseline+400ms target over-throttled the first experiment: 66.261/103.039Mbps at max706.583ms. Runtime now uses a 900ms operational Echo target with 100ms headroom beneath the current strict 1000ms application gate. The authenticated client NOOP baseline remains visible in diagnostics; calibration runs once on an existing socket before control traffic, bounded to2s. A calibration failure closes/retries the unusable initial carrier through existing policy, never leaves a blocked constructor. The server records an unloaded transport Echo if available; otherwise baseline is unknown, never falsely reported as zero-latency proof.

Authenticated logical receipt deltas drive both directions' feedback. Duplicate/invalid receipts, abort and GC do not add useful-byte telemetry; receipt still does NOT authorize physical release. Pump's callback performs only an atomic counter update. Independent actor epochs keep floor recovery alive when all producers are blocked. Below9.5Mbps the limiter bypasses itself; rearming needs3 sustained samples above12Mbps, preserving the old9–11Mbps hysteresis. No guaranteed rate can be promised on a physically slower path.

Two defects in the old integration were corrected: the worst RTT sample retains its own timestamp, so a newer fast Echo cannot reapply an old congestion brake; recent bulk demand keeps feedback active even if a fast VPS APPEND completes between actor ticks. Idle Echos do not throttle bulk. A10s stale feedback condition removes the limiter. Global pressure only limits DATA starts: no quota return, retry of started APPEND or new connection cap is introduced.

## Owner and credit fixes restored after environment loss

Unmeasured authenticated owners are estimated from the current healthy owners' median RTT, avoiding arbitrary1200ms pinning to one early measured lane. OPEN still prefers at most3 near-equivalent owners with the existing active-stream score. A64KiB shared logical/connection credit reserve protects short reads; bulk classification is fixed at the socket read and preserved through partial admission. No protocol window is enlarged.

FIELD31 archive and raw logs were lost before durable saving. These changes were reconstructed from the recorded implementation, starting with the exact saved FIELD30 archive. Do not attribute old FIELD31 measurements to this archive. The primary paired baseline is exact saved FIELD30 runtime with the same three current harness files. A single reconstructed FIELD31 run is only an exploratory reference, never an exact recovered FIELD31 binary or qualified median.

## Model and validation boundaries

Both comparison binaries use `Client.Open` for application connections. Mobile impairment is active before supervisor bootstrap, avoiding stale near-zero initial RTT samples. The local fixture still models a reliable ordered byte stream with synthetic provider behavior, not a real cellular TCP congestion controller or live mailbox service. Only a fresh Android/provider application measurement can qualify a release.

A3s diagnostic probe cadence/deadline lets a30s byte window finish; acceptance stays strictly<1000ms. Strict60s checks use1s cadence/deadline and include sustained last30s bandwidth. Three alternating pairs per profile are required before median comparison; any >10% median regression vetoes release. All failures and rejected prototypes remain in the evidence. Final results are in `ITOM_FIELD32_REPORT.md` and `ITOM_FIELD32_TEST_RESULTS.zip`; this document does not claim checks before they finish.
