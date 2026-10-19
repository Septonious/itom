# Healthy-server reference and stress tests

The reference separates four things which the previous combined benchmark conflated:

| Component | Reference | Status |
| --- | --- | --- |
| Server to Rambler propagation | 20 ms RTT | User-reported route |
| Phone to Rambler propagation | 40 ms; sensitivity 20, 50, 100 ms | User reports typical 20–50 ms, almost always <=100 ms |
| Server uplink | 1000 Mbps full duplex | Configurable strong-uplink assumption, not a measurement |
| Phone access | 600 Mbps reference; low-to-high client profiles | Configurable scenario |
| IMAP backend processing | 2 ms per command | Configurable unmeasured assumption; separate from propagation |
| Compute | Actual runner CPU/quota and Go runtime selection | Shared test process; not eight-core server emulation |

Propagation and bandwidth apply to the actual byte stream below TLS. Each endpoint has an independent account-shared access link, so server traffic is not charged to the phone bottleneck. A client speed profile changes only client bandwidth; server RTT/capacity remain unchanged. The endpoint role is marked after fixture authentication, before application OPEN; this is application transport qualification, not a phone LOGIN latency benchmark.

The reference adds no synthetic server outage, slow disk, replica lag, idle expiry or fixed 80 ms command delay. Real journal fsync, physical session limits, durable charges and proof verification still run. Existing tests verify legacy shaper behavior and the new endpoint isolation. Authenticated TLS NOOP calibration must distinguish the configured 20 ms server path from a separately configured client path.

## Suites

- Required functional/race checks: accounting, unknown outcomes, proof authority, cancellation, protocol identity and client-side interruption recovery with the server still reachable.
- Required normal load: fresh/retained SINGLE, 4 and 32 streams, two complete 60-second rounds; client RTT sensitivity; startup and low-to-high capacity on the client path only.
- Optional legacy stress: deliberate all-account 30-second provider blackout and 1.8-second replica delay with reader restart. `.github/workflows/itom-rc10-legacy-stress.yml` runs these explicitly. They remain preserved rather than being described as normal server conditions.

The primary workflow keeps its original exact 75 Mbps full/tail floors and 750 ms maximum RTT. Probe completeness, per-second positive duplex, original sockets and post-load checks stay. The standalone runner defaults to strict 100 Mbps, with no tolerance, and now also applies the hard 750 ms gate. `--profile legacy-stress` explicitly selects old combined conditions; stress results are labeled separately.

A one-second phone interruption is different from a server outage. Only the deliberately interrupted original client carriers may exit, plus the exact authenticated reattach transition of their old server peers. Replacement or unrelated carrier failures still fail. The healthy server performs a real IMAP command while the phone is offline, and the same application socket must recover afterward.

An earlier client capture contains successful FETCH command durations around 15 ms median and 45 ms p95. APPEND medians are around 55–59 ms. These include IMAP processing, publication and workload effects; they do not independently establish pure TCP RTT. They support using the user's 20–50 ms normal client range rather than applying a blanket 80 ms delay to every command at both endpoints.

Hardware limits and profile assumptions are part of evidence. A passing run on a smaller shared runner is a result for that runner, not a manufactured eight-core result. Real Rambler and physical Android validation remain separate.
