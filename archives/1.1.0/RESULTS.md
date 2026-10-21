# Workload-aware delivery model: laboratory decisions

Current deployment correction (2026-10-21): primary tests now use configurable server network RTT, default20ms. The measurements below used2ms unless explicitly noted. They remain historical, not20ms measurements. See [current server-path comparison](experiments/delivery-model/SERVER_PATH_RESULTS.md). Production controller timing is measured; no server propagation constant changed.

Current testing source is staged by stage.py as ITOM1.1.0 DEV3, package org.itom.client/versionCode10103. It is experimental and not a stable/field-qualified release. Final1.0.0 remains RC2. No historical qualification threshold or physical accounting rule is reduced.

## Design implemented

Each endpoint/direction models authenticated useful delivery independently. Sparse traffic keeps its separate short reservation and demand-scoped receive/coalescing policy. Sustained demand grows flight on useful ACKs; absolute short-ACK/RTT age alone no longer halves bulk. A bandwidth/feedback model guides bounded startup and exploration; the refined policy preserves established bulk flight through mailbox supply cycles. Confirmed published-flight capacity decline or a collective stall can reduce admission; a fully drained epoch returns to its bounded startup seed. The16MiB hard cap, authenticated receiver credit, journal/wallet/proof and unknown-outcome charges are unchanged.

This is a mailbox-specific admission model informed by BBR, not a full BBR/BRUTAL implementation. It has no newly implemented publisher pacing stage or raw packet loss compensation. Lower RTT still requires receive scheduling/provider command work; congestion control alone cannot eliminate the IMAP command turns.

## Trials retained and rejected

- First model, commit5be0d740bc9e7ac68d28e907e292a87707a1f4a5, [run39621414070](https://github.com/Septonious/itom/actions/runs/39621414070): all fresh and changing-route rounds completed, but no useful throughput win and weaker MULTI/retained tails. Not selected. [Raw rounds and samples](experiments/delivery-model/results-v1.json).
- First supply proxy, commit9de16a6200af2dbbb76233d63783a82284d14782, [run39624632542](https://github.com/Septonious/itom/actions/runs/39624632542): superseded because uncompleted APPENDs are not necessarily waiting for quota. It also recorded changing-route tail/RTT failures. No result is hidden. [Raw evidence](experiments/delivery-model/results-v2-supply-proxy.json).
- Grant-aware refinement, commit923b23ade85318239b807d1641f56bf3451aa7c8: waits for grant-backed carrier placement are distinct from admitted network/service work. Low supply-limited samples cannot erase the network estimate. Targeted logic/authority/race/recovery tests passed; matched performance follows below.
- Runtime integration, commit922545c0fe3860693fc9a4c95e9cee73145cef69: real client/server/rotation factories select the model; legacy direct factories preserve the unchanged RC2 contract tests. Added explicit new-model factory, real-supervisor, small-budget and partial/duplicate ACK/retirement checks. Full correctness and testing builds are [run39628803015](https://github.com/Septonious/itom/actions/runs/39628803015). Do not substitute prior source results for this exact build.

## Sustained grant-aware comparisons

[Run39625447401](https://github.com/Septonious/itom/actions/runs/39625447401). Immutable RC2 production plus identical fixture code, compiled before measurement and run serially in reversed orders on each four-core shared runner. Four complete60s rounds per variant/scenario; original full/30s/10s tails, complete probes, socket identities and100Mbps/750ms checks retained. Rates are mean upload/download Mbps of complete rounds; small differences do not establish statistical equivalence.

| Scenario | RC2 up/down | Model up/down | Historical gate |
| --- | ---: | ---: | --- |
| single | 163.0 / 310.7 | 162.0 / 310.3 | Both PASS |
| multi | 165.3 / 323.3 | 165.8 / 326.0 | Both PASS |
| mobile-variable | 109.6 / 276.2 | 109.7 / 277.4 | Both PASS |
| retained-single | 76.6 / 322.4 | 76.8 / 327.0 | Both FAIL100Mbps upload/tail target |
| retained-multi | 76.7 / 312.3 | 76.7 / 314.2 | Both FAIL100Mbps upload/tail target |

Changing mobile RTT is40/80/180/60/140/45/120/180/70/40ms every1.5s. The server path stays2ms/1Gbps. All40 sustained rounds completed, with zero missed/timed-out scheduled probes in recorded rounds and unchanged socket/carrier identities. Retained-state upload remains about77Mbps for both; the controller does not claim to repair that physical-credit/cleanup ceiling.

A separate [provider-service sensitivity](https://github.com/Septonious/itom/actions/runs/39625798028) keeps network routes unchanged and varies IMAP command processing. At20ms service: RC2 about173.97/231.93, model173.91/233.18Mbps; both pass. At80ms service: RC2 about148.98/91.23, model149.18/93.87Mbps; both fail the unchanged100Mbps download/tail goal. An excellent server/ICMP path does not establish equally fast mailbox service. These are sensitivity inputs, not measured Rambler backend delays.

## Full-flight and return to short requests

[Run39626376349](https://github.com/Septonious/itom/actions/runs/39626376349), controller source923b23a with test-only changes in d9ae655806e1c23c28a7a4797b29c2f0bc31bee7. Four concurrent verified4MiB exchanges exercise up to16MiB demand and retain the same probe TCP before/during/after. Original3s exchange deadlines remain. No controller/transport reset is inserted.

- Fixed40ms route: all four invocations pass. RC2 bulk158.46/159.32Mbps; model165.33/164.25Mbps each direction. Post-bulk mean/p95: RC2100.2/136.1ms; model102.7/140.3ms. Bandwidth improved in this screen; short tails did not uniformly improve.
- Varying40–180ms route: both model invocations pass at102.26/110.27Mbps. RC2 times out in its first4MiB exchange; its second invocation passes at103.77Mbps. There is no complete control bulk/post-bulk distribution for the failed invocation and no rate is invented for it. Model post-bulk mean/p95 is165.7/346.1ms over48 requests; RC2 has only24 completed post-bulk samples. Phase timing samples different positions in the dynamic trace, so pooled means are not a controlled proof of lower propagation overhead.

The earlier256KiB closed-loop screen reached only20–40Mbps. Its passes remain recorded, but it is not used as proof of recovery after saturation.

## Exact DEV3 idle/light requests

Two reversed pairs,48 requests per phase/variant, original3s deadlines and sockets. Mean/p95/max in milliseconds. This is the final staged runtime/identity rather than a substituted prototype:

| Client path | RC2 idle | DEV3 idle | RC2 light | DEV3 light |
| --- | ---: | ---: | ---: | ---: |
| 5ms | 53.4/92.2/119.0 | 35.1/57.7/83.6 | 53.3/106.2/231.0 | 36.7/73.6/96.1 |
| 40ms | 102.4/115.2/210.7 | 94.1/141.5/168.7 | 104.5/141.6/204.8 | 100.3/164.4/196.2 |
| 180ms | 333.7/401.0/465.3 | 322.4/401.5/824.2 | 302.6/389.9/658.4 | 293.3/381.6/401.2 |
| 40–180ms | 198.8/385.5/729.9 | 182.8/340.2/380.3 | 168.1/293.5/362.4 | 167.9/327.5/698.2 |

All invocations completed, but some tails worsened:40ms light p95 and variable light p95/max, and180ms idle max824ms. The strongest repeated mean benefit is on the5ms route and includes the independent DEV2 receive/coalescing changes. The results do not demonstrate elimination of idle/light spikes, a universal sub200ms application RTT, or a field bandwidth fix.

## Correctness provenance and remaining work

The first local all-model-constructor full run failed14 tests. Eleven asserted the retained legacy controller contract; a real bug also rounded an explicit48KiB budget upward and was fixed with a model admission regression test. Two functional mobile/restart failures remain recorded. The integrated suite keeps every legacy assertion on its actual controller, adds explicit new-model tests, and uses the model in real supervisors. No test is removed/skipped or given a larger deadline.

A separately compiled, serial local replay on the two-core host passed narrow-mobile recovery for both; RC2 then failed the ramp HTTP deadline, while the current model passed all ramp/cancel/chat phases. One replay does not prove deterministic superiority. The previous failed full run is not called passed. The complete exact DEV3 CI suite now passes: model tunnel738.233s and RC2 reference742.622s, plus vet, required races, controller/supervisor race checks and the worst4G replay at0.324402Mbps each direction (75 verified exchanges, zero failures). Android82 domain and7 app tests pass; lint has zero errors/one dependency-version notice; both JNI ABI identities pass. Linux amd64 was independently rebuilt and is bit-identical to the CI binary. Exact metadata and hashes are in DEV3_BUILD.json.

Full/tail100Mbps retained-state failures and real Android/Rambler validation remain unresolved. Sparse tail variance also prevents an all-situations latency claim. Temporary debug signing does not assure in-place updates; preserve wallet data. Stable1.0.0 remains unchanged.

## Testing downloads

- [DEV3 APK](https://github.com/Septonious/itom/actions/runs/39628803015/artifacts/12360365671), org.itom.client/versionCode10103; SHA256 `bb392c1a38108adc4e8ce0c85868051fe55863fcb0c7ff592f6650db6af51d91`.
- [DEV3 Linux amd64 and arm64](https://github.com/Septonious/itom/actions/runs/39628803015/artifacts/12358599228).

These are unqualified testing artifacts, not a stable1.1.0 release. Persistent signing secrets were absent, so the APK has a temporary debug signer; do not erase wallet data to bypass an update signature mismatch.
