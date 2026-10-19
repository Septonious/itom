# RC2 and RC10: identical healthy-server comparison

Both versions passed all six normal load scenarios and preserved their carriers and original application sockets. The comparison does not establish that RC10 is uniformly faster or lower latency. Both versions retain low-capacity startup and higher-client-RTT limitations. No release qualification failure is waived.

[Paired load run](https://github.com/Septonious/itom/actions/runs/39239603639) · [corrected shared core run](https://github.com/Septonious/itom/actions/runs/39240291399) · [machine-readable rounds and probes](paired-rc2-rc10-39239603639.json)

## Method

- Original RC2 production: tag `v1.0.0-rc2`, commit `a50a6a9e31ee9b38b53e84c9dce645e7ed7e415b`. Every one of its 238 non-test core files is verified unchanged. RC10 production is similarly verified against its checkpoint; comparison changes only test files.
- Each scenario compiles both test binaries before load, then runs the two versions serially on the same four-core shared runner. Version order alternates across scenarios. Each invocation starts with fresh endpoint state or its specified retained-state seed. These are laboratory measurements, not physical Android/Rambler certification or an eight-core hardware benchmark.
- Identical common fixtures: independent below-TLS server20ms/1000Mbps and phone40ms/600Mbps, provider service2ms. Server capacity and processing are assumptions. Account count10, streams1/4/32, two60-second rounds, two-second warmup, retained-state and physical UID history unchanged. Client20/50/100ms and client-only10→20→50→150/20→50→150Mbps profiles use the same original one-round cases.
- Same exact75Mbps full/30-second-tail/10-second-tail regression floor; no percentage margin; maximum loadedRTT750ms; complete probes, useful duplex every second, unchanged carrier epochs, original and fresh post-load sockets. The low-capacity profile separately requires latency/continuity, without making a75Mbps claim on a10Mbps link. Passing the75Mbps regression floor does not certify sustained100Mbps.
- Optional diagnostic fields absent in original RC2 are logged as unavailable through a shared test-only helper; measurements and acceptance do not use them. A common mailbox fixture correction is described below.

## Normal reference: fresh and retained state

Rates are useful upload/download Mbps, averaged across both complete rounds. Tails are the minimum upload/download rates among all original30-second and10-second tails. RTT means use the60 scheduled loaded probe samples (integer millisecond log precision); peaks use the original nanosecond acceptance measurements, rounded here.

| Scenario | RC2 up/down | RC10 up/down | RC2 minimum tails | RC10 minimum tails | Mean RTT RC2/RC10 ms | Peak RTT RC2/RC10 ms |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| fresh-single | 180.8/236.2 | 184.4/242.0 | 182.8/237.0 | 184.0/241.8 | 178/179 | 235/256 |
| retained-single | 102.3/248.8 | 107.6/251.1 | 84.0/228.6 | 83.9/258.6 | 148/153 | 302/223 |
| fresh-four | 175.7/232.0 | 168.0/220.9 | 171.0/218.4 | 165.8/199.8 | 162/203 | 442/426 |
| retained-four | 160.6/203.0 | 162.1/202.1 | 162.1/197.7 | 161.2/200.5 | 151/160 | 216/229 |
| fresh-multi | 168.4/211.3 | 177.3/221.9 | 162.3/209.9 | 177.2/219.7 | 198/183 | 547/282 |
| retained-multi | 141.7/200.3 | 151.8/191.0 | 123.9/189.2 | 128.4/187.0 | 162/159 | 255/234 |

All24 normal measurement rounds combined (12 per version) preserved60/60 probes per two-round case, with zero spontaneous lane outages. Every normal case also passed its original post-load checks.

## Client-path and startup comparison

| Scenario | RC2 result, up/down Mbps | RC10 result, up/down Mbps | Mean RTT RC2/RC10 ms | Peak RTT RC2/RC10 ms |
| --- | ---: | ---: | ---: | ---: |
| client20 | PASS, 179.0/179.2 | PASS, 182.9/177.8 | 126/144 | 185/215 |
| client50 | PASS, 82.3/249.8 | PASS, 83.2/238.2 | 149/149 | 222/231 |
| client100 | FAIL, 71.3/198.8 | FAIL, 70.5/199.8 | 302/256 | 486/378 |
| startup-single | PASS, 191.5/254.3 | PASS, 188.9/250.9 | 162/169 | 235/273 |
| start10 | FAIL, — | FAIL, 5.6/6.4 | —/875 | —/1000 |
| start20 | FAIL, 78.0/77.0 | FAIL, 70.6/76.4 | 323/343 | 1350/1080 |

The10Mbps RC2 case failed its second cold probe before measurement. RC10 reached measurement but timed out on probe3 after6.5 seconds; its partial rates are not a complete60-second performance result. The20Mbps ramp cases completed their30 probes, but exceeded the unchanged RTT limit. The100ms cases completed all probes and kept their carriers, but both missed the upload/tail floor.

## Interpretation and selection

- Fresh MULTI improved in this pair: RC10 full rates177.3/221.9 vs RC2168.4/211.3Mbps, with peakRTT282 vs547ms. Retained MULTI traded approximately10.2Mbps more upload for9.3Mbps less download. It is not an across-the-board win.
- Fresh four-stream performance moved the other way: RC10 rates168.0/220.9 vs RC2175.7/232.0Mbps, and meanRTT203 vs162ms. The trials do not isolate which retained change or run variability caused that difference. Do not declare every retained patch beneficial from this result.
- The100ms upload ceiling and low-capacity ramp failures also occur in unmodified RC2. They cannot be attributed solely to post-RC2 transport changes. The50ms RTT failure in the preceding unpaired RC10 run did not recur in this pair; the earlier failure remains recorded and is not erased.
- Retained SINGLE tail upload remains around84Mbps in both versions. The tests therefore do not certify sustained100Mbps everywhere, even though their original75Mbps regression gates pass.
- Production transport policy remains unchanged by this comparison. No new controller, framing, quota or recovery experiment is promoted. Further changes must be isolated against this RC2 control and improve the target case without degrading the others. APK/server packaging remains blocked by failed required qualification cases.

## Common core checks

The initial core comparison was not fully like-for-like: RC2 retained a historical four-second IDLE parent deadline against the unchanged eight-second watchdog; RC10 already had its corrected12-second fixture. That RC2 test failure is a fixture mismatch, not evidence of a production regression. The preparer now copies the exact corrected mailbox test onto both unmodified production trees. Only the core job is rerun; valid load results are retained. Corrected shared core run39240291399 PASS for BOTH versions: vet, endpoint calibration, original-TCP client recovery, all journal/wallet/flow/datapath/mailbox checks, shared focused regressions and race/proof checks. The corrected preparer supplies12 byte-identical common test files, including the mailbox fixture. The production manifest and both green logs are preserved beside this report.
