# DEV5 matched laboratory results

Runtime source21cf237482f4e57c83c8a3f8841d6808c28e5ffe. Sustained and short-sequence run[39843841636](https://github.com/Septonious/itom/actions/runs/39843841636); idle/light run[39844338335](https://github.com/Septonious/itom/actions/runs/39844338335). Original reference production is unchanged. Shared fixtures and all three production-source manifests were independently checked. No threshold was relaxed.

## Qualification counts

| Test | RC2 | DEV4 | DEV5 |
| --- | ---: | ---: | ---: |
| Original sustained duplex rounds | 13/20 | historical comparison only | 17/20 |
| Short sequential phases | 42/48 | 42/48 | 48/48 |

Every planned sustained round and short phase completed. All candidate startup OPENs and probes met their deadlines; the short phases have no empty500ms window after the first2seconds. The three candidate sustained failures remain failures: two variable40–180ms-path throughput tails at95.735 and98.042Mbps, and one loaded RTT maximum819.105ms. All20candidate round sockets and probes survived. Historical DEV4 20/20 was a different invocation; compare DEV4 directly only in the matched short and idle tests here. Workflow comparison jobs can be red because either a reference or the candidate failed; an overall all-green performance workflow is not claimed.

Retained simultaneous-duplex download still trails matched RC2:210.1 versus229.2Mbps in SINGLE,223.6 versus249.6Mbps in MULTI. Upload improves in both cases. This inherited throughput tradeoff also remains unresolved.

## Short no-warmup transfer averages

All figures are simulator application goodput, not expected field speeds. Routes are client20ms/600Mbps, server20ms/1000Mbps.15accounts; one has64missing-wallet historical reservations. Four configured account sessions exercises the two-socket endpoint topology; ten allows wider isolation. A processing sensitivity uses APPEND50ms/FETCH10ms/STORE+EXPUNGE25ms. Each row averages three12-second phases per direction.

| Streams / sessions / processing sensitivity | RC2 D/U Mbps | DEV4 D/U Mbps | DEV5 D/U Mbps |
| --- | ---: | ---: | ---: |
| 1-service0-sessions10 | 290.3/259.1 | 307.7/271.6 | 319.2/284.7 |
| 1-service0-sessions4 | 280.0/196.5 | 313.1/208.2 | 320.4/232.3 |
| 1-service1-sessions10 | 213.7/206.7 | 220.6/207.0 | 241.0/225.5 |
| 1-service1-sessions4 | 177.2/86.8 | 180.3/91.1 | 197.0/124.4 |
| 16-service0-sessions10 | 289.8/268.6 | 315.3/285.2 | 318.7/293.1 |
| 16-service0-sessions4 | 272.7/191.6 | 307.4/204.9 | 314.9/221.5 |
| 16-service1-sessions10 | 198.6/209.1 | 240.1/228.9 | 241.5/235.7 |
| 16-service1-sessions4 | 175.5/75.9 | 215.4/89.9 | 197.3/119.8 |

SINGLE capped processing case:197.0/124.4Mbps versus DEV4 180.3/91.1. MULTI capped processing case trades download215.4→197.3Mbps for upload89.9→119.8Mbps; the8.4% download regression is a remaining limitation, not hidden by the pass count. The other seven short-profile download averages and all eight upload averages improve over DEV4.

## Idle/light RTT means

Each phase has24original32-byte exchanges with a750ms deadline, after all accounts are ready. All576requests across all three variants completed. Runtime production matches the testing build; the idle workflow adds only a shared test and corrected experiment provenance.

| Sessions / processing sensitivity | Phase | RC2 mean ms | DEV4 mean ms | DEV5 mean ms |
| --- | --- | ---: | ---: | ---: |
| sessions10-service0 | idle | 119.9 | 100.0 | 99.1 |
| sessions10-service0 | light | 118.1 | 93.4 | 88.8 |
| sessions10-service1 | idle | 235.1 | 205.0 | 198.1 |
| sessions10-service1 | light | 234.8 | 229.1 | 199.8 |
| sessions4-service0 | idle | 168.8 | 151.8 | 134.4 |
| sessions4-service0 | light | 166.9 | 144.6 | 137.7 |
| sessions4-service1 | idle | 324.6 | 270.8 | 284.3 |
| sessions4-service1 | light | 318.2 | 277.3 | 301.2 |

All four idle/light means improve versus RC2. Against DEV4, three profiles improve or are approximately unchanged, while the capped processing case worsens: idle270.8→284.3ms, light277.3→301.2ms, and idle maximum403.7→555.3ms. This does not satisfy a universal spike-free idle goal. Full raw samples and failed gates remain in[RESULTS.json](RESULTS.json).

Full Go suite, vet and required accounting/controller races passed on CI job120051751318 (tunnel725.632seconds). Local final shared-socket/recovery/reader race checks passed1.908seconds. Worst raw4G test passed:0.274281Mbps each direction,63exchanges, zero failed, with the user-provided21–84ms direct RTT trace,1Mbps down/2.5Mbps up and20ms server propagation. Production journal/wallet/pool/wire/secure/recovery/session/flow/datapath files remain byte-identical to DEV4. Android and binary build metadata are recorded in ../../DEV5_BUILD.json when complete.

An earlier pre-fairness candidate scored18/20against RC2 10/20 and a local processing-sensitivity run timed out. Those iterations are retained in branch history and their Actions artifacts. The final candidate is selected for concrete recovery/queue fixes, direct short-test gains and passing correctness checks; real Android/Rambler improvement remains unverified until the next field run.
