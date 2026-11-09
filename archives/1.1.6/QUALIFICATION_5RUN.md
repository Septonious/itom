# ITOM 1.1.6 RC1 — five-pair synthetic performance results

**Verified benchmark run:** [GitHub Actions #42636826612](https://github.com/Septonious/itom/actions/runs/42636826612)  
**Exact tested commit:** `c3c6fdbfb9a91433846f35f6975fbf3bbc62527e`  
**Release status:** experimental; not live-device or energy-qualified.

Five independent paired runs per scenario used the unchanged exact 1.1.5 baseline and the same 1.1.6 candidate source. Every run recorded actual application TLS echo RTT, not just the configured IMAP RTT. Per-run details and printed distributions remain in the linked Actions job logs.

## home

```text
=== AGGREGATED MEASURED RTT: home (5 paired runs) ===
idle: median-of-medians 76.894 -> 72.894 ms; p95 (median of runs) 104.137 -> 97.708 ms (-6.429); pooled p95 105.104 -> 97.804 ms (-7.300), 150 candidate observations
loaded: median-of-medians 210.992 -> 212.834 ms; p95 (median of runs) 364.576 -> 368.780 ms (+4.204); pooled p95 362.157 -> 354.698 ms (-7.459), 170 candidate observations
after: median-of-medians 71.061 -> 72.505 ms; p95 (median of runs) 98.053 -> 101.641 ms (+3.588); pooled p95 97.265 -> 99.711 ms (+2.446), 150 candidate observations
upload: median throughput 300.82 -> 307.33 Mbps; candidate minimum 299.34 Mbps
download: median throughput 326.20 -> 330.13 Mbps; candidate minimum 327.63 Mbps
Wrote verified measured RTT aggregation: bench-home-aggregate.json
```

## home-icmp

```text
=== AGGREGATED MEASURED RTT: home-icmp (5 paired runs) ===
idle: median-of-medians 45.758 -> 44.264 ms; p95 (median of runs) 75.043 -> 77.309 ms (+2.266); pooled p95 74.804 -> 74.937 ms (+0.133), 150 candidate observations
loaded: median-of-medians 210.023 -> 201.740 ms; p95 (median of runs) 398.494 -> 333.953 ms (-64.541); pooled p95 398.494 -> 339.178 ms (-59.316), 170 candidate observations
after: median-of-medians 43.733 -> 41.147 ms; p95 (median of runs) 75.795 -> 73.554 ms (-2.241); pooled p95 76.772 -> 80.596 ms (+3.824), 150 candidate observations
upload: median throughput 308.75 -> 297.20 Mbps; candidate minimum 268.50 Mbps
download: median throughput 330.15 -> 340.28 Mbps; candidate minimum 336.66 Mbps
Wrote verified measured RTT aggregation: bench-home-icmp-aggregate.json
```

## mobile

```text
=== AGGREGATED MEASURED RTT: mobile (5 paired runs) ===
idle: median-of-medians 130.686 -> 122.783 ms; p95 (median of runs) 237.347 -> 247.327 ms (+9.980); pooled p95 235.552 -> 247.327 ms (+11.775), 150 candidate observations
loaded: median-of-medians 312.424 -> 300.006 ms; p95 (median of runs) 683.330 -> 790.694 ms (+107.364); pooled p95 767.857 -> 797.175 ms (+29.318), 200 candidate observations
after: median-of-medians 131.517 -> 125.272 ms; p95 (median of runs) 228.436 -> 243.598 ms (+15.162); pooled p95 229.450 -> 243.598 ms (+14.148), 150 candidate observations
upload: median throughput 58.57 -> 59.47 Mbps; candidate minimum 58.70 Mbps
download: median throughput 102.79 -> 103.39 Mbps; candidate minimum 102.86 Mbps
Wrote verified measured RTT aggregation: bench-mobile-aggregate.json
```

## home-contention

```text
=== AGGREGATED MEASURED RTT: home (5 paired runs) ===
idle: median-of-medians 102.881 -> 95.082 ms; p95 (median of runs) 125.312 -> 122.128 ms (-3.184); pooled p95 128.441 -> 125.743 ms (-2.698), 150 candidate observations
loaded: median-of-medians 216.590 -> 223.717 ms; p95 (median of runs) 349.128 -> 418.384 ms (+69.256); pooled p95 348.399 -> 352.992 ms (+4.593), 170 candidate observations
after: median-of-medians 97.344 -> 98.144 ms; p95 (median of runs) 118.396 -> 123.722 ms (+5.326); pooled p95 120.946 -> 124.010 ms (+3.064), 150 candidate observations
upload: median throughput 282.50 -> 295.64 Mbps; candidate minimum 292.13 Mbps
download: median throughput 320.86 -> 318.98 Mbps; candidate minimum 315.14 Mbps
Wrote verified measured RTT aggregation: bench-home-contention-aggregate.json
```

## Interpretation

The 15–30 ms home model improves pooled idle p95 from **105.104 ms to 97.804 ms**, idle median from **76.894 ms to 72.894 ms**, and loaded pooled p95 from **362.157 ms to 354.698 ms**. Median throughput is **307.33 Mbps upload / 330.13 Mbps download**, with all candidate home runs above **200 Mbps** in both directions.

The 3 ms home-floor profile models the user's ICMP observation to `imap.rambler.ru` as a physical-network floor; it must **not** be interpreted as an actual 3 ms IMAP round trip. Its pooled loaded p95 improves by approximately 59 ms.

Mobile (30–150 ms IMAP-path model) has a better idle median but pooled idle p95 is about 11.8 ms worse and loaded p95 about 29.3 ms worse. These are recorded limitations. They remain within modest proportional differences but require live mobile field tests before stability claims.

The snapshot is **not** proof of a sub-100 ms RTT on all networks or a replacement for phone CPU, wakeup, quota, fault, or multi-hour soak checks. Stable 1.1.5 must not be overwritten.
