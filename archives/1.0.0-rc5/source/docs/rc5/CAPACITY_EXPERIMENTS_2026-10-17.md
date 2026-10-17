# RC5 10-account downstream capacity experiments — 2026-10-17

**Classification:** development laboratory, **not a released runtime, not a field/provider result, not qualification**.

Baseline: original RC2-based RC5 transport with authenticated cold-ramp work; Go 1.27.1 Linux amd64, 10 accounts, 24 application streams per direction, 60-second measurement, 80 ms simulated IMAP command cost, modeled 600 Mbps provider, `GOMAXPROCS=4` on a container capped to 4 CPUs. Profiles with increased capacity were transient changes to isolated disposable test source/journals only. They are NOT committed to the transport source tree or available as a migration/install option.

| Case | Full download Mbps | Full upload Mbps | Last 10s download | Last 10s upload | Maximum loaded RTT ms | Verdict |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| Established 12 MiB RC5 baseline | 71.539 | 148.295 | — | — | 436 | Below down floor |
| Sweep-local cached decoded cleanup proof | 73.549 | 134.187 | 71.513 | 152.227 | 419 | Minimal downstream gain, not adopted |
| Same baseline with 6 Go threads under 4-CPU quota | 73.203 | 150.903 | 73.925 | 152.148 | 481 | No material gain |
| Protect only one fast carrier during cleanup | 77.691 | 140.990 | 83.100 | 147.508 | 685 | Rejected, excessive RTT cost for gain |
| Experimental 32 MiB symmetrical data profile | 96.447 | 43.874 | 95.473 | 7.288 | 751 | Rejected: severe upload starvation and >750 RTT |
| Experimental 32 MiB profile with 8 MiB remote grant share and 24 MiB local share | **103.368** | **34.441** | **107.925** | **7.157** | **853.981** | Download milestone only; severe upload and RTT failures |

## What the result actually proves

The physical sender can sustainably exceed 100 Mbps downstream in the modeled workload when legitimately allocated more outbound DATA journal capacity. The test retained authentic APPEND, wallet accounting, UID cleanup proof, cryptographic verification, and real TLS/IMAP fixture operations. There were no missed or timed-out probes. It does **not** establish a viable duplex operating point: the client-side logical delivery controller shrank the upstream window near 699 KiB with observed bulk age approaching 2.8 seconds during the 32 MiB asymmetric run. The client rate fell to about 8 Mbps late in the run. The 32 MiB profile must NOT be applied to deployed accounts or migrated journals.

The previous 16 MiB profile produced down 80.845/up 112.337 Mbps and max RTT 547ms; no larger profile is justified as a default. The bursty 10-account results also show that a brief >100 Mbps peak does not imply full-window/tail qualification. A reported successful download-only metric never substitutes for sustained symmetric throughput and latency.

## Next engineering objective

Preserve RC2 carrier recovery, per-account quota and physical proof invariants, two fast cleanup spares, eight physical bulk writers, and the 12 MiB deployed profile. Investigate adaptive **duplex** admission/fairness using authenticated per-direction backlog/ACK-age measurements so the server cannot starve client upload. A capacity increase alone is unacceptable.

The original 12 mandatory 10-account SINGLE/MULTI cases (three independent repeats in fresh and retained states) must all meet **100 Mbps download AND upload**, every maximum loaded RTT **strictly below 750ms**, zero missed/timed-out probes, and original/fresh connection survival. Startup ramp checks still apply. Aspirational goal 125 Mbps sustained each direction.

**No GitHub release/tag, production migration, APK or deployable server binary is authorized by this experiment.**
