# RC2-based idle/light RTT screening

All five timing prototypes are archived; none is promoted into final1.0.0. Tiny dispatch and faster polling produced some lower means, but neither consistent latency-tail improvement nor a passing loaded non-regression result was established. Keep the exact patches and failures as research, not as accepted optimizations.

## Method and limits

Original RC2 production, ten real client/server supervisors, actual TLS/IMAP and fsync, independent phone5/40ms500Mbps and server2ms1Gbps, provider service2ms. Host exposes3 CPUs but its cgroup quota is2 cores; this is a shared process running both endpoints and provider, not an8-core server model. No injected server outage, replica lag or artificial journal delay.24 verified32-byte requests per idle/light phase; sparse800ms pauses interspersed with150ms think time. Light background is a separate4096-byte verified echo stream with100ms think time, not a fixed offered bitrate. Every request retains its3s deadline and original socket. The background request can fail even when all sampled foreground probes returned; the whole test then fails.

Shared endpoint/test-only fixtures are identical across variants. Their patch is separately restore-verified; all six complete experiment trees reconstruct exactly from RC2. Compile happened before each measurement sweep. Initial runs are hypothesis screening, not randomized statistical evidence. Confirmation used three orders AB/BA/AB on the40ms path; failed/incomplete runs remain in results.json. This small sample does not establish a statistically reliable improvement.

| Variant | Only production change | Decision |
| --- | --- | --- |
| control | None | Immutable RC2 comparator; it also exposed timeouts in this new workload. |
| tiny | Bypass5ms coalescing for<=512-byte reads | Reject: one idle mean improved but a light background request timed out and tails worsened. |
| hot | Cap hot empty polling using half measured provider baseline,2–20ms clamp | Reject: light5ms result improved in isolation; idle/40ms tails worsened. |
| combined | tiny + hot | Reject: multi-second tails; combining two unproved knobs is not a solution. |
| event | Prefer IDLE immediately during a hot empty response search when provider baseline<=50ms | Reject:40ms idle/light tails worsened; notification-only waiting is not uniformly better. |
| micro | Preserve coalescing, cap it at1ms only for<=512-byte reads | Defer: promising first run did not reproduce consistently, loaded non-regression unproved. |

## Completed phase measurements

Mean/p95/max RTT in milliseconds. PASS/FAIL is the whole invocation, including background and carrier checks. An incomplete case is not replaced by a nearby good result.

| Run | Phase | Mean | p95 | Max | Invocation |
| --- | --- | ---: | ---: | ---: | --- |
| combined-rtt40.log | idle | 162.0 | 330.3 | 459.5 | passed |
| combined-rtt40.log | light | 478.5 | 1760.8 | 2153.0 | passed |
| combined-rtt5.log | idle | 364.2 | 1811.0 | 2019.5 | passed |
| combined-rtt5.log | light | 430.9 | 1187.3 | 2907.6 | passed |
| confirm1-control-rtt40.log | idle | 122.8 | 165.1 | 177.9 | passed |
| confirm1-control-rtt40.log | light | 117.0 | 152.5 | 191.4 | passed |
| confirm1-micro-rtt40.log | idle | 124.7 | 241.9 | 317.9 | passed |
| confirm1-micro-rtt40.log | light | 129.6 | 208.9 | 246.2 | passed |
| confirm2-micro-rtt40.log | idle | 353.7 | 1464.0 | 1565.8 | passed |
| confirm2-micro-rtt40.log | light | 325.6 | 805.0 | 1611.1 | passed |
| confirm3-control-rtt40.log | idle | 229.3 | 327.3 | 942.9 | passed |
| confirm3-control-rtt40.log | light | 195.9 | 313.9 | 347.8 | passed |
| confirm3-micro-rtt40.log | idle | 211.6 | 317.7 | 1573.4 | passed |
| confirm3-micro-rtt40.log | light | 111.1 | 162.4 | 173.9 | passed |
| control-rtt5.log | idle | 99.1 | 188.6 | 191.2 | passed |
| control-rtt5.log | light | 127.1 | 289.2 | 482.6 | passed |
| hot-rtt40.log | idle | 627.7 | 1734.0 | 2537.2 | passed |
| hot-rtt40.log | light | 716.2 | 2564.7 | 2623.8 | passed |
| hot-rtt5.log | idle | 137.5 | 700.7 | 748.8 | passed |
| hot-rtt5.log | light | 81.5 | 115.5 | 121.5 | passed |
| second-control-rtt40.log | idle | 186.3 | 329.8 | 724.1 | passed |
| second-control-rtt40.log | light | 214.6 | 517.8 | 1077.9 | passed |
| second-event-rtt40.log | idle | 444.9 | 2323.4 | 2590.7 | passed |
| second-event-rtt40.log | light | 361.8 | 1068.0 | 2182.4 | passed |
| second-event-rtt5.log | idle | 168.9 | 360.4 | 1166.0 | passed |
| second-event-rtt5.log | light | 81.5 | 138.3 | 182.7 | passed |
| second-micro-rtt40.log | idle | 146.4 | 211.9 | 388.5 | passed |
| second-micro-rtt40.log | light | 147.2 | 256.3 | 638.0 | passed |
| second-micro-rtt5.log | idle | 84.1 | 229.7 | 256.1 | passed |
| second-micro-rtt5.log | light | 83.5 | 172.9 | 239.7 | passed |
| tiny-rtt5.log | idle | 69.1 | 126.0 | 137.5 | failed |
| tiny-rtt5.log | light | 186.2 | 641.6 | 2108.5 | failed |

## Throughput control

A separate four-stream duplex control requested two complete60s rounds with the existing strict100Mbps full/tail target and750ms maximum loaded RTT,30 scheduled probes per round, independent endpoint paths, real sockets, original journals and unchanged acceptance code. Both RC2 and micro failed before completing the first60s round: control timed out probe3; micro timed out probe10. Their partial rates are not reported as sustained throughput, nor interpreted as a proven speed improvement. No target/deadline was lowered to produce a pass. The75Mbps historical floor was not substituted for this100Mbps attempt.

Because the reference itself fails and does not reproduce the user's known good RC2 throughput, this local2-core combined-host model is not yet a calibrated predictor of the new deployment. The cause is unresolved; do not assert a faulty production server or blame only CPU. Before accepting a1.1 controller, reproduce representative RC2 rates and timing on calibrated fixtures and the actual new server, then rerun independent paired latency/throughput controls. Existing old4-core CI comparisons and real-provider evidence remain separate.

## Reproduction

From the repository with original tags available, use Go1.27.1 and `python3 docs/1.1.0/experiments/reproduce.py --work /path/to/scratch`. It compiles every variant before running AB/BA sweeps, keeps exit statuses, and returns failure if any invocation fails. It does not run a passing release qualification on your behalf. `run-confirmation.sh` records the exact confirmation and loaded-check environment for this run. All original thresholds remain untouched.
