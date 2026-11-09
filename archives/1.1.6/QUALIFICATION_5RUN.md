# ITOM 1.1.6 RC1 — mandatory ASCII85, five paired trials

**Verified completed run:** [GitHub Actions #42650945537](https://github.com/Septonious/itom/actions/runs/42650945537)  
**Exact transport source:** `52ce71e52846b3f188a0751f4b5c95a394cf3e78`  
**Status:** experimental / not field-qualified. The 1.1.5 stable tag remains unchanged.

This source retains a single **7-bit ASCII85 MIME codec** for every IMAP publication including Noise handshake, data, control, and recovery. It removes Base64/dual-codec logic and negotiation. The short-lived 20 Mbps pacing-guard experiment introduced after this commit failed full safety tests and was **reverted**; the release source has the original tested controller.

Five independent paired runs per profile exercised actual TLS application echo through simulated IMAP mailboxes. Pooled RTT p95 is calculated over every recorded application RTT, not input RTT. A successful synthetic result does not demonstrate live Rambler or device energy behavior.

| Profile | Idle median baseline→candidate | Idle pooled p95 | Loaded pooled p95 | Upload Mbps baseline→candidate | Download Mbps baseline→candidate |
|---|---|---|---|---|---|
| home | 75.561→75.623 ms | 102.75→98.59 ms | 307.14→276.024 ms | 312.53→339.54 | 327.4→361.6 |
| home-icmp | 46.142→47.038 ms | 78.543→76.624 ms | 326.86→425.052 ms | 290.46→302.75 | 339.3→366.89 |
| mobile | 120.941→128.732 ms | 245.288→232.333 ms | 774.142→671.902 ms | 58.28→64.3 | 101.63→115.75 |
| mobile-20 | 123.791→120.934 ms | 243.041→237.272 ms | 1038.149→959.698 ms | 3.27→3.6 | 13.07→14.36 |
| home-contention | 94.793→94.498 ms | 128.024→119.237 ms | 292.398→256.209 ms | 313.39→345.83 | 327.69→358.74 |

**Mobile-20:** The model limits the mobile shared physical link to 20 Mbps down/5 Mbps up. Its throughput improves from **13.07 to 14.36 Mbps down** and **3.27 to 3.60 Mbps up**. Median loaded RTT is higher (463.656→530.074 ms) even though pooled loaded p95 improves from 1038.149→959.698 ms. Both facts should be considered in field testing.

**Home:** The full-capacity home model sustains **339.54 Mbps up / 361.60 Mbps down** while its pooled loaded p95 drops 307.140→276.024 ms. Five-run minimum candidate upload remains 264.63 Mbps, above the 200 Mbps floor.

**Known regression:** The 3 ms network-floor profile has **higher loaded p95**, 326.860→425.052 ms (+98.192 ms), despite better throughput. The 3 ms floor is the user’s ICMP observation to `imap.rambler.ru`, not a real IMAP round trip. The change is deliberately **for field testing only**, not promoted to stable.

**Real-device checklist:** Match 1.1.6 server and Android app; record RTT idle, loaded and post-load with measured upload/download; capture radio throughput and battery/CPU usage; verify resume, DNS, mailbox reclamation and sessions over hours; compare against unchanged 1.1.5 using the same provider accounts and environment. Never delete the 1.1.5 release or production state.

## Raw benchmark log excerpts

### home

```text
=== AGGREGATED MEASURED RTT: home (5 paired runs) ===
idle: median-of-medians 75.561 -> 75.623 ms; p95 (median of runs) 102.750 -> 99.777 ms (-2.973); pooled p95 102.750 -> 98.590 ms (-4.160), 150 candidate observations
loaded: median-of-medians 193.409 -> 186.575 ms; p95 (median of runs) 313.843 -> 276.024 ms (-37.819); pooled p95 307.140 -> 276.024 ms (-31.116), 170 candidate observations
after: median-of-medians 71.685 -> 70.855 ms; p95 (median of runs) 101.279 -> 98.857 ms (-2.422); pooled p95 98.583 -> 98.857 ms (+0.274), 150 candidate observations
upload: median throughput 312.53 -> 339.54 Mbps; candidate minimum 264.63 Mbps
download: median throughput 327.40 -> 361.60 Mbps; candidate minimum 357.60 Mbps
Wrote verified measured RTT aggregation: bench-home-aggregate.json
```

### home-icmp

```text
=== AGGREGATED MEASURED RTT: home-icmp (5 paired runs) ===
idle: median-of-medians 46.142 -> 47.038 ms; p95 (median of runs) 78.322 -> 75.202 ms (-3.120); pooled p95 78.543 -> 76.624 ms (-1.919), 150 candidate observations
loaded: median-of-medians 199.444 -> 220.163 ms; p95 (median of runs) 326.860 -> 431.865 ms (+105.005); pooled p95 326.860 -> 425.052 ms (+98.192), 170 candidate observations
after: median-of-medians 44.032 -> 40.627 ms; p95 (median of runs) 82.624 -> 81.994 ms (-0.630); pooled p95 83.754 -> 81.465 ms (-2.289), 150 candidate observations
upload: median throughput 290.46 -> 302.75 Mbps; candidate minimum 295.07 Mbps
download: median throughput 339.30 -> 366.89 Mbps; candidate minimum 351.00 Mbps
Wrote verified measured RTT aggregation: bench-home-icmp-aggregate.json
```

### mobile

```text
=== AGGREGATED MEASURED RTT: mobile (5 paired runs) ===
idle: median-of-medians 120.941 -> 128.732 ms; p95 (median of runs) 230.173 -> 232.333 ms (+2.160); pooled p95 245.288 -> 232.333 ms (-12.955), 150 candidate observations
loaded: median-of-medians 281.974 -> 299.586 ms; p95 (median of runs) 748.915 -> 628.319 ms (-120.596); pooled p95 774.142 -> 671.902 ms (-102.240), 202 candidate observations
after: median-of-medians 126.442 -> 130.820 ms; p95 (median of runs) 236.452 -> 226.778 ms (-9.674); pooled p95 237.619 -> 230.976 ms (-6.643), 150 candidate observations
upload: median throughput 58.28 -> 64.30 Mbps; candidate minimum 63.63 Mbps
download: median throughput 101.63 -> 115.75 Mbps; candidate minimum 114.31 Mbps
Wrote verified measured RTT aggregation: bench-mobile-aggregate.json
```

### mobile-20

```text
=== AGGREGATED MEASURED RTT: mobile (5 paired runs) ===
idle: median-of-medians 123.791 -> 120.934 ms; p95 (median of runs) 235.962 -> 237.272 ms (+1.310); pooled p95 243.041 -> 237.272 ms (-5.769), 150 candidate observations
loaded: median-of-medians 463.656 -> 530.074 ms; p95 (median of runs) 1053.191 -> 963.625 ms (-89.566); pooled p95 1038.149 -> 959.698 ms (-78.451), 285 candidate observations
after: median-of-medians 130.630 -> 119.724 ms; p95 (median of runs) 236.400 -> 225.695 ms (-10.705); pooled p95 240.386 -> 226.885 ms (-13.501), 150 candidate observations
upload: median throughput 3.27 -> 3.60 Mbps; candidate minimum 3.42 Mbps
download: median throughput 13.07 -> 14.36 Mbps; candidate minimum 14.35 Mbps
Wrote verified measured RTT aggregation: bench-mobile-20-aggregate.json
```

### home-contention

```text
=== AGGREGATED MEASURED RTT: home (5 paired runs) ===
idle: median-of-medians 94.793 -> 94.498 ms; p95 (median of runs) 128.519 -> 119.237 ms (-9.282); pooled p95 128.024 -> 119.237 ms (-8.787), 150 candidate observations
loaded: median-of-medians 208.721 -> 195.066 ms; p95 (median of runs) 283.528 -> 256.209 ms (-27.319); pooled p95 292.398 -> 256.209 ms (-36.189), 170 candidate observations
after: median-of-medians 93.279 -> 96.667 ms; p95 (median of runs) 113.136 -> 124.824 ms (+11.688); pooled p95 115.749 -> 124.824 ms (+9.075), 150 candidate observations
upload: median throughput 313.39 -> 345.83 Mbps; candidate minimum 342.30 Mbps
download: median throughput 327.69 -> 358.74 Mbps; candidate minimum 354.62 Mbps
Wrote verified measured RTT aggregation: bench-home-contention-aggregate.json
```

