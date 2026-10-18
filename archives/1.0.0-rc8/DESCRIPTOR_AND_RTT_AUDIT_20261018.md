# RC8 RTT, jitter, and descriptor-disconnect investigation
Date: 2026-10-18 UTC

**Status: experimental reliability fix. RELEASE GATES NOT MET.** Android application ID remains `org.itom.client`; keep physical journals, proof-based mailbox cleanup, accounts, IMAP quotas and logical TCP identities unchanged.

## Real-world diagnosis
- Android/server paired export from 2026-10-18: median physical DATA append 43ms client / 74ms server, p95 110ms / 128ms. Server p95 carrier-selection wait ~206ms and control-message wait ~149ms. Android records physical-wallet exhausted batch outcomes and application stream closures under concurrent speed testing; these are not independently definitive causes.
- Snapshot account RTT 315-506ms; user saw an account at 165ms in another instant. Low per-account RTT supports using responsive lanes for short work, **not** a guaranteed 165ms full application RTT.
- During 10Mbps actual low-link startup, delivery-feedback target allowance reached **2000ms bulk** and **1500ms uncalibrated fast**. Client and server had sizable unacknowledged logical backlog and loaded RTT exceeded 750ms before timeout. Reactive timer reduction alone failed, indicating queued DATA and rate adaptation must be addressed *before* backlog forms.

## Independently tested, rejected latency A/B
| Change | Results | Decision |
|---|---|---|
| Adaptive fast-carrier wait grace max 150ms instead of 650ms | First 4/32-stream 60s runs passed 155/79, 133/79Mbps with ~413/466ms max RTT. Repeated 4-stream 3/3 later failed 71/70/72Mbps down; max RTT once **934ms**. Repeated 32-stream second round had hard carrier exit **DESCRIPTOR_LIMIT**. Low 10/20Mbps startup still failed. | **Do not promote** |
| Loaded RTT penalty on nonbulk ACK/WINDOW selection | 32-stream 132/80Mbps and ~429ms max RTT passed, 4-stream trailing 74.4Mbps and maximum RTT **881ms** failed; SINGLE trailing72.1Mbps failed, constrained start failed. | **Do not promote** |
| Limit fast age allowance to 650ms without an unloaded fast-path baseline | SINGLE 157/80Mbps and 75.55Mbps verified tail passed. 32-stream rate fell to ~120/74Mbps; low 10Mbps stayed connected for 30/30 probes but **max RTT 1414ms**, 20Mbps still timed out. | **Do not promote** |

Raw evidence:
- https://github.com/Septonious/itom/actions/runs/39127903563
- https://github.com/Septonious/itom/actions/runs/39128284396
- https://github.com/Septonious/itom/actions/runs/39128470804
- https://github.com/Septonious/itom/actions/runs/39128733624

## Root cause of one observed hard MULTI failure and targeted fix
The server's `tunnel/lane.go` accepted at most **128 distinct authenticated DATA UsageReport descriptors** in a live lane cache (`l.reports`), while a physical account lease can admit **192 DATA messages**. A second valid physical report cache inventory may remain uncollected until BODY visibility/verified GC completes. The 129th *valid* digest, if no invalid report to evict, would issue `DESCRIPTOR_LIMIT` and disconnect the carrier, disrupting the speedtest.

**Experimental correction:** bounded `dataDescriptorCacheLimit=512` (~2.7 inventories) and regression coverage for 384 distinct valid descriptor receipts. Do not evict valid authenticated reports, issue counterfeit ACKs, change physical grant or journal quotas, or delete unverified DATA. New `CachedDataDescriptors` diagnostic indicates real cache occupancy, separately from `PendingDescriptors` (BODY waiting for its descriptor). This is an operational headroom fix, not permission to ignore a permanently uncollected backlog.

Test correction: readiness proves both grant classes **were published**, not that active workers must leave both grant channels simultaneously nonempty at snapshot time. Require **bounded 5s physical refill plus original account-specific TCP echo**, rather than weaker connected-only assertion. The earlier exact-instant test had intermittent false failure.

**Exact evidence:** https://github.com/Septonious/itom/actions/runs/39129426156

| 32-stream strict scenario (10 accounts, 600Mbps simulated provider) | Upload | Download | Max application RTT | Full/trailing ≥75/75? |
|---|---:|---:|---:|---|
| Repeat 1, 60 seconds | 134.733 Mbps | 80.072 Mbps | 431.8ms | PASS |
| Repeat 2, 60 seconds | 121.521 Mbps | 80.793 Mbps | 412.8ms | PASS |
| Repeat 3, 60 seconds | 134.537 Mbps | 77.739 Mbps | 435.7ms | PASS |

Every 32-stream repeat completed 30/30 RTT probes, no timeouts and original connection survival, **no DESCRIPTOR_LIMIT** in this workload. This evidence supports the change but cannot guarantee no field failures.

| 4-stream strict scenario | Up | Down | Max RTT | Result |
|---|---:|---:|---:|---|
| Repeat 1 | 154.499 | 76.695 | 488.3ms | FAIL trailing floor 73.313Mbps |
| Repeat 2 | 154.656 | 74.781 | 444.5ms | FAIL overall floor |
| Repeat 3 | 154.442 | 74.781 | 433.7ms | FAIL overall/tail floor |

All 4-stream repeats preserved original sockets and replied to all probes. The 10Mbps and 20Mbps genuine early startup tests still FAIL max 750ms/zero timeout requirement; descriptor headroom does not resolve bulk backlog formation.

## Acceptance and next steps
- Full Go/correctness/race suite https://github.com/Septonious/itom/actions/runs/39129527278 must PASS independently before promoting to shared RC8 branch. Until then keep fix isolated.
- For further latency work: instrument admitted bulk in flight and short response per account at 10/20Mbps **before** increasing unacknowledged flight, differentiate true unloaded fast baseline from IMAP ACK-service age; cap early backlog dynamically only if the high-rate 75/75 tail and 4/32 streams remain intact. No global throughput cap or unconditional physical permit refund.
- Re-run all SINGLE/4/32, 3x sustained, 12 fresh/retained, low→high and high→low 10/20Mbps, full recovery and Android field tests. Never change `org.itom.client`, weaken 75/75 or 750ms RTT gates, or claim release stability while these fail.
