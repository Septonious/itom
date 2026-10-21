# Retained-capacity comparison results

All measurements below use real supervisors, TCP/TLS/IMAP, verified application bytes, actual journal fsync and ten accounts. The server propagation input is20ms, independent of the40ms client path or changing40–180ms mobile trace. RC2 production is unchanged; shared fixture corrections and workloads are byte-identical. These are laboratory results, not Android/Rambler field qualification.

The original duplex gates remain100Mbps in each direction over the full60s, final30s and final10s; positive duplex in every1s window; all scheduled probes under the original deadlines; maximum750ms; and preserved application/carrier identities. A loaded spike still fails that historical750ms gate. Additional one-way workloads are reported separately and never counted among the20 duplex rounds. No failed invocation is discarded.

| Remote floor | First paired run, RC2 / candidate | Independent confirmation, RC2 / candidate |
|---|---:|---:|
|900ms|14/20 /17/20|15/20 /17/20|
|1100ms|15/20 /18/20|15/20 /18/20|
|1250ms|14/20 /17/20|not repeated|
|1350ms|16/20 /14/20|not repeated|

The900ms and1100ms candidates repeatedly improve the numerical goal. They were held back because mean retained-state simultaneous-duplex download fell in both cohorts, despite a substantial upload gain. Gentler floors do not solve the full problem:1250ms still loses retained duplex download and1350ms misses the pass-count target. Full Go/vet suites and accounting/controller races pass for all four floors; correctness success does not override a performance failure.

Independent directional capacity comparisons isolate one bulk direction while retaining a persistent probe socket, verified bytes, full60s and complete30s/10s tails:

| Workload | RC2 usefulMbps | Candidate usefulMbps |
|---|---:|---:|
|Fresh upload,1350ms|176.836|177.725|
|Fresh download,1350ms|334.614|335.380|
|Retained upload,900ms|76.885|126.576|
|Retained download,900ms|264.602|265.534|
|Retained upload,1100ms|76.332|105.024|
|Retained download,1100ms|265.589|266.902|
|Retained upload,1250ms|77.160|92.275|
|Retained download,1250ms|257.607|268.278|
|Retained upload,1350ms|76.880|85.739|
|Retained download,1350ms|264.165|267.551|

Both upload invocations at1250ms/1350ms miss100Mbps, as do their RC2 references. One1350ms candidate also has an empty active1s window.900ms/1100ms pass both directional upload invocations, and all download invocations pass. The earlier download regression is specific to the simultaneous-duplex comparisons in these measurements; the one-way results establish neither its mechanism nor universal absence of download regressions.

Idle/light measurements on fresh profiles do not use the historical-debt cleanup adjustment. They measure the bounded request-response polling change. In the latest cohort, pooled48-sample means for RC2→candidate were84.7→56.5ms idle and93.5→56.7ms light at5ms client propagation;148.3→131.2ms and145.9→125.1ms at40ms;229.6→212.9ms and224.2→204.3ms on changing40–180ms. At180ms, idle mean is essentially unchanged370.3→370.8ms and light mean364.1→356.9ms. Earlier cohorts contain mixed high-delay tails; no universal idle-spike elimination is claimed.

Raw per-invocation values, failures, samples, source provenance and log hashes:

- [Primary900/1100ms results](results.json), [workflow39644062304](https://github.com/Septonious/itom/actions/runs/39644062304).
- [Independent confirmation](results-confirmation.json), [workflow39645459471](https://github.com/Septonious/itom/actions/runs/39645459471).
- [Gentler1250/1350ms results](results-gentler.json), [workflow39678756369](https://github.com/Septonious/itom/actions/runs/39678756369).
- [Additional one-way results](results-directional.json), [workflow39679201514](https://github.com/Septonious/itom/actions/runs/39679201514).

The [paired reclamation experiment](../duplex-reclamation/README.md) now tests earlier eligibility for existing outbound proof work. Its hypothesis is that accelerated inbound reclamation makes the quarter-window outbound trigger too late under duplex traffic. This remains a hypothesis until matched results show otherwise. Final1.0.0, immutable RC2 and active DEV3 build artifacts remain unchanged.
