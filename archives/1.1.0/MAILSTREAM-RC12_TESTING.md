# ITOM 1.1.0 MAILSTREAM-RC12 — partial-startup recovery candidate

[RC10 remains the first field-ready baseline](https://github.com/Septonious/itom/releases/tag/v1.1.0-mailstream-rc10).

The delayed-FETCH 100 Mbps upload gate remains **failed** after two attempts:
98.7/103.0 Mbps initially, then 82.7/93.1 Mbps. All four transfers completed
without account failures. This release is intended for startup-recovery testing;
performance is unqualified and the 100 Mbps field upload goal remains open.
The original and repeated results are both included in the evidence.

Both RC11 captures fail on the same four accounts during control setup, leaving
18/30 roles. RC12 opens existing control folders without redundant CREATEs and
retries temporary pre-service account failures on fresh sockets. Recovered
accounts join the running VPN; healthy carriers are not restarted. Diagnostics
include startup attempt counts and next retry times.

RC11 upload-rate protection and RC10 download behavior are retained. No further
increase in initial flight, automatic folder deletion or unaudited capacity
reset is introduced. Provider EXPUNGE stalls observed in the captures remain an
open limitation. DNS settings and the app theme are unchanged.

Install both matching endpoints. Install the APK in place to retain profiles
and settings: app ID and signer are unchanged, version code 10137. Upload the
matching server `.bin` to `/tmp`, then use server-manager option 18, or run:

```sh
chmod +x /tmp/ITOM-1.1.0-MAILSTREAM-RC12-linux-amd64.bin
sudo /tmp/ITOM-1.1.0-MAILSTREAM-RC12-linux-amd64.bin install
```

No manual account cleaning is required. For field comparison, enable logs
before connection and check whether 30/30 roles appear, then run a cold MULTI,
SINGLE and another MULTI test and reconnect the VPN. A temporarily unavailable
account may take several seconds to join; its diagnostic retry record explains
why. Persistent provider-side failures cannot be promised away by a local test.

Controlled acceptance adds separate 50 Mbps down / 35 Mbps up shaping and
four-account startup faults, with frozen RC11 as the failing reference. Synthetic
payload throughput must be compared to those directional limits, including MIME
and control overhead. These results are not live-provider qualification or a
claim of 100 Mbps on a 35 Mbps uplink.

## Final exact-source verification

Source `45052ad0054580c9fb89f5609d7a745e2e38f447` passes the functional/safety checks in the [matched build run](https://github.com/Septonious/itom/actions/runs/42050253417) and [independent source checks](https://github.com/Septonious/itom/actions/runs/42050253387). The delayed-FETCH 100 Mbps gate remains failed after two attempts; RC12 is a startup-recovery testing release with unqualified performance. Normal/race application, native and SDK tests, the IMAP fork checks and vet pass. Android passes 93 domain tests, 8 app tests and lint with zero errors. Both JNI libraries, server binaries and source archive are verified. The persistent Android signer is unchanged.

Six actual-server startup/reconnect cycles pass: three at 30/30 roles and three at 45/45, including server restarts and verified manual-stop local-capacity resets. The RC9 idle feedback and the RC10 slow-receipt upload limit are reproduced against frozen source; pressure clearing, delayed-update ordering, asymmetric full ledgers, active historical deferral and automatic idle cleanup pass.

The following are controlled TLS IMAP/HTTPS models, not live-provider results.

| Model / streams / cycle | Upload Mbps | Download Mbps | Max idle/post ms | Max loaded ms |
| --- | ---: | ---: | ---: | ---: |
| ordinary / 1 / 1 | 284.2 | 313.0 | 98.0 | 297.8 |
| ordinary / 4 / 1 | 270.1 | 323.7 | 87.5 | 422.4 |
| ordinary / 4 / 2 | 270.2 | 329.9 | 87.5 | 422.4 |
| ordinary / 32 / 1 | 277.0 | 318.2 | 95.8 | 344.8 |
| retained control / 4 / 1 | 279.9 | 321.5 | 96.0 | 323.1 |
| retained control / 4 / 2 | 197.6 | 325.6 | 96.0 | 323.1 |
| 40 old folders + metadata cost / 4 / 1 | 268.5 | 188.7 | 171.2 | 361.1 |
| 40 old folders + metadata cost / 4 / 2 | 172.8 | 215.6 | 171.2 | 361.1 |
| 40 old folders + metadata cost / 4 / 3 | 106.9 | 266.3 | 171.2 | 361.1 |
| 350 ms upload FETCH / 4 / 1 | 82.7 | 298.5 | 123.5 | 929.2 |
| 350 ms upload FETCH / 4 / 2 | 93.1 | 300.0 | 123.5 | 929.2 |
| 50/35 Mbps + startup recovery / 4 / 1 | 23.6 | 34.7 | 95.5 | 1360.9 |
| 50/35 Mbps + startup recovery / 4 / 2 | 24.7 | 35.4 | 95.5 | 1360.9 |

The first delayed-FETCH check measured 98.70 and 102.97 Mbps upload. One cycle missed the 100 Mbps gate; the initial failed gate and raw evidence are preserved alongside one repeat for variability. The table above describes that repeat. These runs do not establish every-window sustained 100 Mbps.


The 7 Mbps shared-link cancellation/recovery model completes at 4.76/4.46 Mbps upload/download, with maximum idle/post RTT 109.7 ms and loaded RTT 1615.9 ms. No account fails or is rejected. RC10 remains the first field-ready release; RC12 requires a new field upload test.

The corporate fault model reproduces 18/30 roles on frozen RC11 and reaches 30/30 on this source after temporary SELECT refusals, without blocked existing-folder CREATE or healthy-account restarts. Two directional 50/35 Mbps cycles complete within the idle/loaded RTT gates. This is a synthetic replay, not a live provider guarantee.

The account-interference gate completes three full speedtests above 100 Mbps phase averages, with no active historical LIST. Ordinary fast-path full-phase and idle/loaded RTT gates pass; the delayed-FETCH upload 100 Mbps gate remains failed. Strict pause/rolling-throughput misses remain in the raw evidence and metadata; all sustained-window and live-provider goals are not claimed complete.
