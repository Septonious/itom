# ITOM 0.9.2-Rev1 — transport replacement and field qualification plan

Prepared 2026-10-13. Baseline: **0.9.1-Rev10 / `transport-rework2-rev10`**. Next release: **0.9.2-Rev1**. This is a plan for one coordinated implementation session, with internal checkpoints and one final candidate. Archive analysis and selected baseline checks have been verified; the proposed transport and 100/100 Mbps performance have not yet been implemented or qualified.

## 1. Authority, scope, and decisions

The current user's request governs this work: investigate the archive and prior ITOM context, plan a substantial transport replacement, lift the ten-account profile limit, replace misleading performance tests, compare account/folder schemes, and target sustained **100 Mbps application download + 100 Mbps application upload with application RTT strictly below 1000 ms**, including high load. Implementation belongs to the next session.

Attached `CODEX_TASK.md`, old plans, and recovered memory are reference material. Their instructions to implement Rev11, prefer small patches, accept 30/30 first, deploy, or reuse old version names do not override this request. Historical 30/30 and 50/50 results are diagnostic milestones, never final acceptance. No production update, account/mailbox purge, quota reset, publication, or release is part of this planning session.

Adopt these design decisions:

1. Replace the transport orchestration, stream admission, account scheduling, delivery feedback, batching, and receive/control loops as one coherent design. Retain independently useful security, IMAP parsing, journal/wallet, routing, manager, and Android components behind explicit adapters and regression contracts.
2. Decouple logical TCP streams from physical account lifetimes. A stream may stripe over any eligible account; carrier failure must not automatically destroy its logical owner.
3. Use equal-capability accounts with separate control and data folders as the preferred topology, bounded optional data sharding, and scheduler-protected control/interactive capacity. Keep a two-folder topology for constrained providers. Do not permanently exclude the two best accounts from bulk.
4. Implement the same send/receive state machine in both directions, with quota-authority adapters where server/client permissions differ. Improve behavioral symmetry without making the client its own quota authority.
5. Remove fixed account-count restrictions across server and Android. Control actual resources through byte/connection/operation/memory budgets, not an arbitrary account count.
6. Replace the performance harness before accepting optimization claims. Real Android/provider results are a separate mandatory qualification layer.
7. Preserve historical physical debt, ambiguous APPEND accounting, exact cleanup evidence, authentication, isolation, replay protection, stream ordering, and supported persistent state. Faster transport must not obtain speed by declaring old debt free.

The proposed default is a topology, not a claim that adding folders creates bandwidth. Its activation must pass the provider/session calibration and matched comparison below. A calibrated two-folder mode is an acceptable provider-specific result if it satisfies the same user goals.

## 2. Recovered ITOM context and provenance

No persistent-chat-memory retrieval tool was available. Context was recovered from `/workspace/ITOM_RECOVERED_MEMORY.md`, `/workspace/ITOM_IMAP_REWORK_PLAN.md`, the older `itom-handoff`/`itom-package` records, and the new uploaded archive. Those local notes include a complete retained Markdown inventory. Their old session status and missing-file claims are dated observations, not current facts: the new archive supplies raw Rev10 diagnostics, screenshots, and multiple newer source snapshots previously absent.

### 2.1 Relevant history

| Period | Recovered outcome | Lesson for this rework |
|---|---|---|
| Early WSIT | Historical user reports above 50 Mbps both ways; retained references to P025/P026, BDP2, QVFT1. Exact successful source and matched field conditions are not established here. | Use as a lead, not a reproducible baseline or a performance promise. |
| FIELD35–50 | Many transport/batching/controller experiments and synthetic successes. | Keep correctness discoveries; avoid treating a historical peak as sustained qualification. |
| FIELD51–67 | Asymmetry, idle RTT, intermittent throughput, BINARY/accounting experiments, journal-scope failures. | Separate encoding, delivery, and physical accounting measurements. |
| FIELD68–71 | Best recorded FIELD68 synthetic 36.272/35.013 Mbps with RTT max 559 ms; reproducibility weaker; window/control tuning did not establish the later goals. | A constants-only change has already failed to resolve the architecture. |
| FIELD72–77 | Build identity fixes, ACK indexing, recovery isolation, canonical MIME-size fix. FIELD77 synthetic 32.129/25.016 Mbps with max RTT 529 ms failed symmetric 30/30. | Preserve MIME/physical-proof and mixed-proof recovery regressions. Build success is separate from transport qualification. |
| FIELD78 | Retained negative research: approximately 10.154 DL-only, 19.958 UL-only, 7.497/8.668 duplex in full windows; complete source not established in the recovered smaller handoff. | Preserve failures rather than importing unqualified tuning. |
| 0.9.1 Rev1–2 | Initial IMAP rework; retained-debt grant startup defects and recovery changes. | Fresh journals can conceal failures that occur on upgraded installations. |
| Rev3–5 | Smaller recovery grants, reconnect/read changes, ACK/controller and network-policy fixes. | Recovery, idle estimates, and tiny setup packets need one shared model. |
| Rev6–7 | Cold TCP/TLS startup and 200 Mbps model; per-account freshness and short-path affinity. | Synthetic access capacity and privileged short paths do not establish real app capacity. |
| Rev8 | ITH9 cumulative authenticated STATUS, feedback affinity, mapped IPv4 fix, native host fixture, manager improvements. | Retain monotonic progress coalescing and routing fixes; native host tests still omit phone TUN behavior. |
| Rev9 | Build/UI/diagnostics changes; only two of ten accounts reached field readiness. | Authentication does not imply usable transport capacity. |
| Rev10 | Small authenticated recovery-fast grants allow ten-account startup with old reservations intact. Real Android Speedtests still fail throughput, loaded RTT, and sometimes initiation. | Readiness was repaired; sustained usable capacity was not established. |

Recovered architectural hazards include atomic reservation races, self-throttling feedback loops, stale idle epochs, inadequate stream flight, repair traffic bypassing budgets, and acknowledgements blocked by application writes. Treat these as explicit regression scenarios, not proof each exists unchanged in Rev10.

### 2.2 Verified source identity

| Item | Verified value |
|---|---|
| Upload | `ITOM_Codex_Handoff_Rev10_Field_Failure_2026-10-13.zip`, 31,132,388 bytes |
| Upload SHA-256 | `ed3068cedd7b1ea2ac21d27d79b9217ebab5fb27cd37eeab1dc7d11d2a7a736b` |
| Outer archive | 189 entries; all 188 files listed in `SHA256SUMS.txt` match |
| Selected baseline ZIP | `source/current/ITOM-0.9.1-Rev10-sources.zip` |
| Baseline ZIP SHA-256 | `290ad961da0699d6f84c80e6381849a9ed45fb40418f0e58543efcbbf9149508` |
| Nested source | 656 entries; all 654 listed source-manifest files match |
| Historical sources | Rev1, Rev3–Rev9 and FIELD51 supplied; Rev2 has notes but no corresponding source ZIP in this archive |
| Evidence | Original client/server diagnostic ZIPs, four screenshots, archived build reports, trace analyzer, history |

Use paths below relative to the extracted Rev10 `itom/` root unless stated otherwise. Preserve the upload and current source ZIP unchanged. Never select the older FIELD77 source merely because an old plan names it.

## 3. Verified field observations and uncertainty

### 3.1 Screenshot results

All four images were visually checked against the archive's table. Numbering follows `evidence/screenshots/test_*.jpg`; filename-derived device-local times are approximate, not synchronized event markers.

| Image | Approximate Moscow time, Oct 13 | Download Mbps | Upload Mbps | Idle RTT ms | Loaded DL RTT ms | Loaded UL RTT ms | Jitter ms |
|---|---|---:|---:|---:|---:|---:|---:|
| 01 | 18:50:53 | 26.30 | 3.40 | 374 | 758 | 583 | 175 |
| 02 | 18:52:41 | 19.10 | 13.10 | 403 | 1367 | 590 | 147 |
| 03 | 18:53:38 | 20.10 | 13.50 | 447 | 396 | 571 | 269 |
| 04 | 18:55:53 | 4.79 | 11.30 | 357 | 867 | 689 | 198 |

These are completed Speedtest summaries, not raw 30-second application-byte series or simultaneous duplex results. Failed initiations have no numeric throughput result and must be counted as failures, not omitted. SINGLE sometimes exceeding MULTI is an additional user observation; the archive does not establish a matched SINGLE/MULTI experiment or its cause.

### 3.2 Independent trace recount

Fresh parsing found **88,453 client + 121,360 server events**, with no malformed JSON and no byte-identical duplicate lines. Rotation files were retained and events sorted by recorded time. Byte-identical duplicate detection does not prove there are no semantic duplicates; repeated retries/events can legitimately describe the same work.

- Client coverage: 18:54:12.326–18:55:56.254 Moscow time, about 103.93 seconds.
- Server coverage: 18:53:18.050–18:56:44.020, about 205.97 seconds.
- Common recorded timestamp range: the client range above. Cross-host clock alignment is unverified. Most earlier screenshot tests are not fully covered.
- The exported client diagnostics snapshot is after VPN OFF. Its zero rate is not evidence of zero traffic throughout the tests.

| Full retained endpoint trace | Client | Server |
|---|---:|---:|
| Successful `data_batch_append` events | 1,244 | 1,546 |
| All `control_batch_append` events | 5,444 | 5,987 |
| Successful `control_batch_append` events | 5,441 | 5,986 |
| Successful control/DATA APPEND ratio | 4.374 | 3.872 |
| Mean canonical encoded DATA batch bytes | 41,135 | 11,455 |
| DATA batches containing one record | 1,014 / 1,244, 81.5% | 1,240 / 1,546, 80.2% |
| DATA APPEND p50 / p95 / p99 ms | 46.384 / 104.134 / 169.905 | 74.225 / 170.208 / 258.058 |
| DATA APPEND max ms | 341.562 | 444.780 |
| Full DATA completion p99 / max ms | 165.702 / 343.173 | 1045.261 / 3576.905 |
| Stripe-selection wait p95 / max ms | 0.665 / 317.253 | 328.828 / 9756.641 |

In the common recorded timestamp range, the server has 1,001 successful DATA APPENDs and 4,414 successful control APPENDs, ratio **4.410**. The client ratio remains 4.374. Therefore the high command ratio is not explained solely by the endpoints' different capture lengths.

Do not add `imap_end append_data` to `data_batch_append`: they describe overlapping stages of the same physical work. Encoded-size counters here are canonical MIME accounting sizes, potentially different from actual BINARY literal or TLS wire bytes. Control `decoded_bytes` is also not a physical-wire denominator. Neither metric is application goodput.

The leading client control records are MAIL_RECEIPT 2,579, GRANT_USAGE 1,337, STATUS 1,284, GRANT_RELEASE 605, GRANT_RETIRE 469. Server leaders are GC_STATUS 3,011, MAIL_RECEIPT 2,962, STATUS 1,688, GRANT_RECONCILE 1,092, GRANT_RETIRE_ACK 638. Records can share one physical control APPEND; these counts must not be interpreted as separate messages.

Other observed pressure:

- Client DATA completion includes 72 `WALLET_EXHAUSTED`; client/server delivery admission includes 249/283 deferred events in the archived recount. Exhaustion can mean a legitimate insufficient grant, unavailable replacement, delayed proof, or historical reservation; it is not automatically leaked credit.
- Old grant reservations include 85, 85, 176, 168, 170, 159, 159, 173, 162, 172 DATA slots. The 192-slot recovery fixture has at most two allocatable fast/bulk slots at 178 retained slots, and refuses when insufficient slots remain. Startup with two slots cannot imply sustained speed.
- Sampled server health reaches 177–192 held DATA messages across accounts. These are sampled/cumulative states, not a complete reconstructed physical ledger.
- Tiny-account output differs sharply: client a05/a08 produced about 125/86 KB canonical DATA over the retained interval, while a01 produced 13.44 MB. Reserved-reader/traffic-class placement can cause this; unequal bytes alone do not prove provider account throttling.
- Sampled positive client health RTT medians differ, approximately 328–802 ms across accounts, with only six positive samples per account; a06 has a 1148 ms sample. These are transport diagnostic values of mixed age/load, not paired application RTT under identical conditions.
- Reader wake-to-fetch has only nine client and three server samples, reaching 1603.762/1124.341 ms. They establish possible long wakes, not a complete wake-latency distribution.

### 3.3 Findings to distinguish from hypotheses

| Finding or hypothesis | Evidence status | Investigation that resolves it |
|---|---|---|
| Control/cleanup command amplification | High control/DATA ratios directly reproduced | Command and actual-wire budgets per phase, useful payload, and calibrated provider ops/s |
| Delay outside DATA APPEND | APPEND max ~445 ms versus completion ~3.58 s and selection ~9.76 s on server | Matched batch critical-path trace; do not subtract unrelated maxima as a causal decomposition |
| Historic debt suppresses usable capacity | Retained reservations and exhausted/deferred events confirmed | Per-account, per-class physical ledger with reconciled outcomes and authoritative free byte/message counts |
| Current topology intentionally skews bulk use | Two reserved accounts excluded by source | Compare scheduler capacity reservation with whole-account reservation on identical workload/debt |
| Provider accounts intrinsically have different speeds/RTT | Not established | Randomized matched account probes, same operation class, both endpoints, repeated across time |
| MULTI introduces extra control, contention, small batches, or reordering | Plausible; current captures do not isolate these causes | Matched 1/4/8/16/24/32-flow sweep; counters for contention, framing, feedback, reorder occupancy |
| Android/native path creates the synthetic/field gap | Not isolated | Direct path → public Go transport → native host → actual Android TUN ladder |

Do not announce a single root cause before that isolation. The evidence supports replacing coupled scheduling and feedback, but it does not prove folders alone or a larger window will meet the target.

## 4. What the existing implementation actually does

| Concern | Source anchor | Verified behavior and redesign implication |
|---|---|---|
| Account ceiling | `core/servercfg/accounts.go:44`, `config.go:84`; `core/manager/manager.go:152,227` | Server add/validate reject >10; manager shows `/10` and rejects adding the eleventh. |
| Different client ceiling | `android/domain/.../Validation.kt:17`; `android/app/.../ProfileEditorScreen.kt:166`, `MainActivity.kt:53` | Android permits 1–64 and disables add at 64. Remove both fixed limits coherently. |
| Logical flight | `core/tunnel/delivery_control.go:18` | 1 MiB initial, 64 KiB minimum, 16 MiB session bulk maximum, 128 KiB fast reserve. The older plan's 3 MiB session ceiling is stale. |
| Stream window | `core/tunnel/lane.go:49` | Each stream has 3 MiB credit. Session and stream limits are different constraints. |
| Delay target | `delivery_control.go:349–371` | ACK floor can reach 2 s and learned target age 2.2 s. This does not alter the test's 1 s application RTT gate, but permits controller behavior that needs explicit latency-budget reconciliation. |
| Account service | `account_work.go` | Local class maxima urgent=2, receive=2, maintenance=1, DATA=2; total defaults to seven, queue bound 32/class. It is per endpoint/process, not a distributed provider/session/ops limit. |
| Whole-account reserve | `reader_reserve.go:89`; `stripe.go`, `local_selection.go` | At least five usable accounts → two responsive accounts reserved and excluded from new bulk. Replacement must protect capacity without unnecessarily removing the fastest DATA carriers. |
| Placement | `account_placement.go`, `carrier_feedback.go`, `stripe.go` | Mixed costs for writer occupancy, unread bytes, debt, queue cost and fresh/idle RTT; old idle samples valid for up to 120 s, pool median fallback, fast-slot grace up to 650 ms. |
| Publication asymmetry | `lane.go:1960–2185` | S2C local journal Prepare/Start/Complete; C2S wallet spend and concurrent GRANT_USAGE descriptor publication. Descriptor and BODY arrival/reconciliation are extra dependencies. |
| Current batch bounds | `wire/wire.go:18`, `wire/data_batch.go`, `datapath/pump.go`, `lane.go:1270` | DATA record <=32 KiB, batch <=16 records, canonical MIME <=512 KiB. Frequent one-record batches mean cap increases alone may do little. |
| Persistent profiles | `storage.go:22–56` | Explicit 8 MiB/128, 12 MiB/192, 16 MiB/256 DATA profiles and controlled migration; default 12 MiB/192, control profile 2 MiB/128. Preserve actual installed profile, not only current defaults. |
| Encoding | `core/mailbox/mailbox.go:838`, `binary_policy.go` | BINARY/literal8 already exists with per-login refusal caching and conservative unknown-outcome handling. It is not a new proposed optimization. Verify actual account capability and wire ratio. |
| Receive/cleanup | `receive_page_policy.go`, `mailbox/batch.go` | Four-to-sixteen UID receive pages and bounded DeleteMany already exist. Remove redundant proof reads only after retaining equivalent identity/deletion evidence. |

The replacement should delete superseded policy paths after equivalence checks. Keep an old baseline mode only in the comparison harness, rather than leaving multiple production controllers competing for admission.

## 5. Capacity and latency mathematics before tuning

### 5.1 Define exactly what 100/100 means

Count useful, independently verified bytes received by applications, in decimal Mbps. Upload is confirmed at the exit application; download is confirmed at the phone/client application. Local writes, APPEND completions, encrypted payload generation, exit reads, and retransmitted bytes are separate counters.

Use the stricter planning assumption: qualify 100 Mbps each way in sequential Speedtest-style phases **and** simultaneous 24-up/24-down load when the physical path permits it. Report SINGLE separately, aim for 100 Mbps on one sufficiently provisioned stream, and require its feasible-window efficiency even on capacity-limited profiles. Never multiply 100 Mbps by account count or demand 100 from every individual account.

At 100 Mbps, payload rate is 12,500,000 bytes/s:

| Verified feedback/credit-return time | Minimum useful flight for 100 Mbps |
|---:|---:|
| 100 ms | 1.25 MB |
| 350 ms | 4.375 MB |
| 500 ms | 6.25 MB |
| 800 ms | 10 MB |
| 1000 ms | 12.5 MB |

This is pipeline flight, not permission to queue one second of avoidable work. Size logical windows to propagation/feedback plus bounded service jitter, while controlling queued work separately. For a 3 MiB stream window, theoretical window/rate ceilings are about 71.90 Mbps at 350 ms, 50.33 at 500 ms, 31.46 at 800 ms and 25.17 at 1000 ms. Actual return times must be measured; diagnostic RTT is not automatically the flow-credit delay.

New per-stream windows must borrow from a global bounded buffer budget. A single stream can borrow enough for its verified BDP; 48 streams must not each allocate a permanent large window. Separate receive acceptance, application consumption, transport delivery acknowledgement, and physical deletion credit.

### 5.2 Carrier expansion and the two endpoint legs

Let `E` be measured carrier byte expansion per useful byte, including the selected encoding and protocol overhead. Ordinary base64 is at least 4/3 before MIME/TLS/control. Verified BINARY may lower wire expansion, while canonical journal charge remains a separate value.

- Phone uplink needs at least `E * R_up`; phone downlink at least `E * R_down`.
- VPS provider-facing downlink needs at least `E * R_up`; VPS uplink at least `E * R_down`.
- A simulated/shared provider uplink aggregating both endpoints needs `E * (R_up + R_down)`, and its downlink needs the same. For base64 and 100/100 duplex, that is >=266.67 Mbps in **each** aggregate direction, before overhead. A single cap on all four traversals needs >=533.33 Mbps.
- A 130 Mbps endpoint leg cannot carry 100 Mbps useful base64 traffic even before overhead: upper bound 97.5 Mbps. Historical 130 Mbps link notes are not a matched direct-path measurement for these captures.

Draw the exact shaper/cap boundaries in every report. Do not confuse a 600 Mbps aggregate test cap with a 600 Mbps phone link, or increase test capacity silently to obtain PASS.

### 5.3 Message rate and reclaim capacity

At 100 Mbps useful, 64 KiB useful DATA batches need ~190.7 DATA messages/s, 256 KiB need ~47.7, and 1 MiB would need ~11.9. The latter is a calculation, not permission to exceed current caps. Use the actual fill distribution, command concurrency, service latency, and provider limits, not just a maximum batch size.

For each account/direction measure:

`goodput <= min(access_payload_capacity, eligible_append_service * payload_per_message, receive_service * payload_per_message, flow_window / credit_return_time, physical_reclaim_service * payload_per_message)`.

Apply shared provider/IP/host limits to the sum, not independently to every account. For steady operation, physical reclaim must match publication over a long interval; otherwise all tests eventually consume the finite journal/mailbox window.

Estimate required physical byte debt as `canonical_publication_rate * time_to_proven_reclaim`, and required message slots as `publication_messages_per_second * time_to_proven_reclaim`, plus unresolved historical reservations and class reserves. Ledger **reserved grant limits** and actually spent/observed attempts are different categories; do not double-count them. Define an additive authoritative partition before making graphs.

If immutable historical debt or an externally imposed cap makes a target impossible, report the limiting quantity. Reconcile with evidence, move eligible new traffic to additional accounts, or explicitly migrate a supported capacity profile. Never fabricate proof or silently inflate physical quotas.

### 5.4 RTT budget

For an ordinary request/response, account for queueing and admission, two DATA publications, visibility, notification/DONE/FETCH, processing, application delivery, and the relevant return legs. Separate local durations from cross-host durations; use bounded clock-offset estimates or causal joins, never subtract wall clocks blindly.

Let `B` be freshly measured unloaded application RTT in the same topology. Maintain a fixed <1000 ms application ceiling. For initial healthy calibration use a **design queue budget** `min(200 ms, max(0, 1000 ms - B - 150 ms safety margin))` across both legs. This is a starting controller objective to verify, not an extra acceptance allowance. If `B + margin` already consumes the budget, report that condition and avoid teaching the controller that 2 s has become acceptable.

Track actual loaded RTT, p50/p95/p99/max, jitter, missed samples, and loaded-minus-idle overhead. Minimize the overhead under matched capacity; do not promise elimination of intrinsic provider/account variance.

## 6. Account/folder alternatives — tradeoffs and selection

The important axes are stream ownership, folder isolation, carrier roles, resource budgets, and recovery. Evaluate them together with the same implementation and calibrated limits; otherwise a topology comparison is confounded by different controllers.

| Scheme | Advantages | Costs and failure modes | Decision |
|---|---|---|---|
| **1A. Two folders, each account owns its TCP streams** | Smallest mailbox layout, fewer readers/SELECTs, simple attribution and recovery | SINGLE limited by one account; slow account stalls its streams; account reconnect threatens owner lifetime; control and large BODY fetches share a mailbox tail | Retain as comparison; unsuitable as the target stream model |
| **1B. Two folders, equal accounts, pooled/striped logical streams** | Simple provider footprint; all accounts usable; stream survival and aggregate capacity | Independent writers do not remove receive BODY head-of-line blocking; control remains behind mixed mailbox work; short DATA may require spare service capacity | Required constrained-provider fallback, using the new scheduler |
| **2. Dynamic DATA folders + separate control folders on every account** | Isolates control mailbox tails; sharding may reduce per-folder indexing/reader bottlenecks; equal-purpose accounts can reconnect independently | More selected readers, folder creation, metadata, UIDVALIDITY domains, dirty-state scans and migration; cannot evade per-account/IP/provider limits; unbounded folders cause churn and polling | Adopt bounded, measured sharding; reject uncontrolled per-load folder creation |
| **3. Dedicated DATA and CONTROL accounts, e.g. 4+1** | Control may obtain separate account-level bandwidth/ops/debt and independent reader path | Single control account can strand four data accounts; shared provider/IP cap may nullify isolation; cross-account quota controls need authenticated routing and original-ledger authority; duplicate controls consume real quota | Experimental; 4+1 with no failover cannot be the reliability default |
| **3B. Dedicated data + at least two control-capable accounts** | Better account-level isolation and failover than 4+1 | Sacrifices data capacity/accounts; requires bounded idempotent failover, deduplication and carrier-aware physical accounting; two accounts may still share the same failure domain | Evaluate only if account-level isolation is measured to help |
| **4. Hybrid equal accounts, separate control folders, soft control preference** | Keeps every account capable of both roles; isolates control tails; no fixed control-account single point; shortest viable feedback path can change without transferring stream ownership | More complex than two folders; needs negotiated layout, bounded session budgets and exact physical routing; preferred-account rotation can itself cause jitter | **Preferred architecture**, subject to calibration |
| **5. Direction-specialized accounts** | Can exploit proven direction-specific capacity differences | Wastes idle capacity in sequential tests, makes failure asymmetric, complicates recovery and static profile management | Allow dynamic directional weights; avoid permanent direction-only ownership |
| **6. Multi-provider pool** | Potential independent quotas/backend failure domains | Current profile validation forbids mixed providers; extra configuration, trust, invitation and diagnostic changes; may not bypass the phone/VPS access bottleneck | Later optional extension; not required to achieve this rewrite with one provider |

### 6.1 Preferred bounded layout

Example negotiated namespace for each account/user:

```text
<namespace>/v2/C2S/control
<namespace>/v2/S2C/control
<namespace>/v2/C2S/data/00
<namespace>/v2/S2C/data/00
```

Start with one DATA shard/direction. Experimental shard counts 2 and 4 require a measured per-folder service benefit and enough aggregate account sessions. Folder hierarchy and exact names depend on provider delimiter/capability; do not assume nested CREATE semantics. Keep the current two-folder names as legacy drain targets.

All accounts retain DATA+CONTROL capability, regardless of whether configured, starting, ready, degraded, reconnecting, or draining. Eligibility is per direction/class, not one permanent boolean for the entire account. Choose a responsive primary and alternate for logical control service using observed completion, headroom and hysteresis. Do not reserve two entire accounts from bulk: reserve bounded service/buffer capacity; lend idle capacity when latency protection remains possible.

Large DATA cannot share a selected receive connection with critical control if its non-preemptible BODY can violate the control deadline. Separate control folders therefore require a separate watching/reading path, not merely two names selected alternately on one overloaded connection. APPEND can target a folder without making it the selected mailbox; legal command sequencing still applies.

Ordinary tiny application DATA also needs a bounded fast path. Specify a negotiated small-DATA lane: either permit size/rate-limited short DATA envelopes in the fast/control mailbox, or dedicate a small-DATA shard if real session capacity permits. Do not place a large batch there. Probes receive exactly the same classification as equivalent user traffic. Test unprioritized DATA and short responses arriving on a bulk stream as well, so privileged diagnostic routing cannot hide DATA starvation.

### 6.2 Shard changes and recovery

Use a durable authenticated layout epoch and explicit folder manifest. Both peers learn new folders before publication. No new shard is eligible until readers, quota binding and capabilities are ready. Shrink by stopping new placement and draining old shards; retain unresolved messages and UIDVALIDITY mappings. Never MOVE/RENAME in-flight DATA to simplify accounting without a fully specified proof transformation.

Only activate another shard after backlog/service evidence persists across several observation epochs; only remove it after a substantially longer quiet/drain interval. Freeze initial epochs/minimum dwell times before the A/B experiment. Avoid account-count × folder-count polling or a new shard per connection/burst. Emergency reconnect is independent of layout rebalance.

The physical byte/message limit is shared over all new and legacy folders on an account/ledger, not multiplied by the number of shards. Snapshot/restart tests must preserve that union. Two-folder fallback is a negotiated topology change with a drain rule, not a silent retry of ambiguous messages in a different folder.

### 6.3 Aggregate session budget

Account limits usually cover both endpoint logins and all users. Local seven-operation budgets do not enforce that. Maintain a negotiated conservative connection allocation for client/server and supported users, including bootstrap, reader, writer, GC and reconnect overlap. Essential control/receive slots are allocated first; borrowable DATA workers and extra shard readers use the remainder. Budget old+new sockets during rotation.

An illustrative single-user four-folder layout can require four persistent roles per endpoint: control RX, DATA RX, control TX and DATA TX, **eight total account sessions**. Maintenance borrowing, extra workers, second users, and reconnect overlap must fit separately. This is an accounting example, not the default allocation. For a provider allowing only five total sessions, it cannot be used unchanged: negotiate fewer persistent writers/borrowed maintenance, or use the two-folder layout. Never create a separate ten-session pool at each endpoint and assume a ten-session provider cap is respected.

Provider/host/IP operation limits may be shared across many accounts. Apply rate and reconnect budgets at those scopes too; distinguish configured limits from observed busy/backoff signals. An account-count increase must not multiply uncontrolled TLS sessions and polls.

## 7. Transport replacement contracts

### 7.1 Logical sessions and physical carriers

Create a new transport module with clear boundaries: logical session/stream actor, delivery and RX credit, account service manager, mailbox reader, physical publisher, reliable control aggregator, cleanup/reconciliation worker, and diagnostics. Proposed filenames are implementation choices; these responsibilities are required.

Logical identity includes authenticated user, session, generation and stream; physical identity includes account, folder/layout epoch, ledger/grant, message/attempt, digest, canonical size and UID/UIDVALIDITY when observed. Carrier epoch is not logical-stream ownership. No message from another owner/generation may become eligible merely because it shares a mailbox.

Use the same ordered transitions in C2S and S2C:

1. Reserve bounded logical flight and scheduler capacity.
2. Acquire an account/folder reservation and applicable quota permission.
3. Fix the physical message manifest and authenticated envelope; durably record charge/attempt before starting APPEND.
4. Publish exactly once for that physical attempt; record accepted, definitively not started/rejected, or uncertain with conservative debt.
5. Observe and authenticate a self-describing body; enqueue bounded per-stream RX work.
6. Emit cumulative logical acceptance/credit progress independently of physical reclamation.
7. Delete only with supported exact evidence; apply physical refund once after proven retirement.

Server-issued remote authorization and server-local publication use different authority adapters but the same orchestration and telemetry. Test the adapters against their existing durable contracts. A sender's authenticated claim is not independent proof of deletion or permission to mint new credit.

### 7.2 Self-describing DATA envelope

Design a versioned authenticated DATA envelope containing the immutable publication identity, authorized grant/lease and ordinal, canonical charge rule, layout/account binding, logical member identities/offsets/lengths, cryptographic digest/binding and bounded optional progress metadata. Receive validation should not wait for a separately carried per-DATA descriptor when the grant is already authenticated and all necessary verification data is present in the DATA envelope.

Removing this critical dependency is a wire/accounting change requiring explicit review. The receiver must verify grant scope/bounds, unique ordinal/attempt binding, exact canonical size, per-member authentication and replay before accepting. An unrecognized lease enters bounded reconciliation, not fast-path acceptance. Old GRANT_USAGE descriptor handling remains only for legacy drain/compatibility. Do not acknowledge durable descriptor registration before its required journal state exists.

Remote authorizations remain cumulative bounded leases; refill before exhaustion from measured spend/return times. Lease sizing must consider bytes **and** message ordinals and the remaining fast/control reserve. Larger leases can reserve scarce debt unnecessarily; use the physical ledger to optimize rather than perpetually requesting fixed-sized grants. Make fast DATA progress possible without multiplying per-stream control grants.

Piggyback monotonic logical progress on available outbound envelopes where scope permits. A pure download still requires timely upstream progress even without upstream DATA; piggybacking must have a finite standalone fallback. Physical quota controls carried through another account retain the original account/ledger identity and original authority. Initially keep physical reconciliation account-local unless the routed contract and failure tests are complete.

### 7.3 Batching and copies

Assemble fair batches across ready streams in the same compatible logical security/physical accounting scope. A multi-owner batch needs an authenticated durable member manifest and per-member crash/cancel/replay outcomes; never lose charge attribution by merging byte buffers alone. Do not batch across unrelated users or grants without a contract explicitly permitting it.

Flush on size, age, deadline, blocked credit or lack of eligible members. Ordinary short setup/interactive DATA flushes promptly; sustained bulk aggregates to amortize APPEND/FETCH and crypto/journal work. Compare bulk assembly age 2/5/10/20 ms and useful targets 64/128/256 KiB within negotiated MIME limits. These are experimental candidates, not forced defaults. Avoid workers draining fragments before they can form useful batches; avoid one worker holding a reservation while waiting for nonexistent input.

Bound a non-preemptible DATA literal by observed serialization/service time. Example design condition: `literal_wire_bytes / conservative_account_wire_rate + extra_service <= data_turn_budget`. If the account is slow, shrink the batch/turn instead of waiting for a full large batch. Body-size caps alone cannot protect latency at low bandwidth.

Encode once where safe; avoid repeated MIME canonicalization, allocation and whole-buffer copying on the critical path. Preserve actual physical bytes or sufficient equivalent immutable proof material for cleanup verification. Benchmark CPU/allocations/fsync as diagnostics, then measure applications before calling them speed improvements.

### 7.4 Receive, reordering, and application delivery

Use notification-driven control readers with predicate-before-IDLE and generation wakeups, bounded audits for lost notifications, UID ranges/cursors, streaming body emission and independent reconnect. DATA readers drain fairly using byte/time-bounded turns, not an unbounded number of UIDs. Count empty fetches and SELECT/IDLE churn; keep global polling and fd/goroutine limits.

Authenticate and route each received member before it can influence another stream. A slow application consumer must not block the mailbox reader, another stream's delivery, or other control updates. Per-stream queues plus a shared byte budget provide backpressure. If the receiver ACKs bounded accepted memory before consumption, specify crash semantics: receipt frees transport flight, not application completion or physical debt; streams lost at restart must fail/resume according to an explicit protocol, never silently claim delivery.

Retain ordered delivery and FIN/half-close/RESET semantics. Use cumulative frontiers plus bounded selective received ranges to avoid full-stream repair from one missing stripe; SACK/selective feedback is conditional on negotiated wire support and independent correctness tests. Retransmit missing logical ranges on healthy carriers under the same service budgets. A new repair publication has its own physical charge; an earlier ambiguous attempt remains charged and may appear later.

Reorder buffer occupancy, oldest missing range, late/duplicate messages and repair amplification are controller inputs. Do not stripe an interactive exchange across slow carriers simply to equalize account bytes. SINGLE bulk may stripe over multiple accounts with bounded reorder exposure; the window/credit design must allow that.

### 7.5 Fair scheduler and atomic admission

One service authority per resource scope accounts for queued and active work, socket limits, provider ops/byte budgets and class protection. Use predicted completion = queue wait + service/serialization + visibility/read delay, with explicit confidence/age, for placement. Keep logical flight and physical quota separate from service reservations.

Use deficit fairness across users/streams for bulk and bounded deadline-aware priority for handshake, critical progress and ordinary interactive DATA. Maintenance must receive a measured minimum reclaim service so debt cannot grow indefinitely. Reconciliation and repair compete within bounded budgets; no hidden bypass on retry paths.

Atomic selection/reservation must prevent concurrent callers claiming the same free capacity. Network/fsync work runs outside scheduler locks. Cancellation, restart and shutdown release each in-memory reservation exactly once, while durable ambiguous debt remains. Generation notifications must wake every eligible waiter without spin or lost wakes.

Do not measure fairness by equal bytes on unequal accounts. Measure useful demand-relative service on eligible accounts/users, bounded starvation, queue age and explanations for ineligibility. Slow paths receive low-rate exploration; stale samples do not make them permanently excluded. All sort/heap/state structures must scale with configured/active accounts, without an O(accounts × streams) scan per DATA record.

## 8. CONTROL overhead and RTT controller

### 8.1 Control semantics and emission

Inventory every control type and define its owner, authenticated scope, reliability, merge key, monotonic fields, trigger, deadline, acknowledgement, retry and physical cost. Preserve existing STATUS monotonic merges; combining only the offset can corrupt independent receipt/credit/terminal frontiers.

Use these rules:

- OPEN/OPEN_OK, initial usable credit, RESET, critical repair and terminal progress are urgent and idempotent within their specified identity. Avoid an additional round trip between OPEN_OK and first receive credit; combine/negotiate safely where possible.
- STATUS/receive credit is cumulative. Replace an unsealed older snapshot with a newer compatible one; never mutate already sealed counter-bound records or erase a mandatory terminal state.
- MAIL_RECEIPT, grant spend/retirement and GC state are distinct physical-accounting semantics. Batch compatible proofs/receipts and progress ranges; do not collapse them into an application ACK or invent missing historical entries.
- Retransmit on state/progress deadlines with backoff and deduplication, not a periodic control per stream forever. Separate state-changing records from repeated identical reports.
- Use bounded independent control bytes/messages/ops reserves. A dedicated folder still shares account capacity; prioritize service before a large literal starts, and respect both endpoints' connection allocation.
- Idle liveness should cost a bounded per-account/global budget, not scale with stream count. Reconnect/account probes are phase-spread and lower frequency when samples are fresh; essential failure detection stays bounded.

For healthy sustained bulk, set an initial target **<1 successful standalone control APPEND per DATA APPEND**, with DATA+piggyback, standalone controls, startup and drain reported separately. Also measure all operations and actual wire bytes per useful MiB. This ratio is an efficiency target, not permission to suppress essential proof/control, and tiny-only workloads need their own denominator. Predefine ops/byte budgets from calibration and reject amplification even when aggregate Mbps looks good.

### 8.2 One documented feedback policy

Implement independent directional estimators and one policy with IDLE, STARTUP/PROBE, STEADY, CONGESTED, OUTAGE and RECOVERY states. Avoid duplicating the earlier overlapping probe/ramp/recovery logic. Specify transitions, hysteresis, limits, no-progress detection and sample classes before coding.

Estimate these separately:

1. Provider/connection operation service distributions, including account/folder/direction/class.
2. Unloaded ordinary DATA exchange RTT and its sample freshness.
3. Logical DATA acceptance/credit return and application consumption progress.
4. Active queue age, reorder/missing-range age, outstanding non-preemptible service and physical reclaim age.
5. Useful delivery capacity, marking app-limited, window-limited, quota-limited, scheduler-limited, provider-busy and fault-limited intervals.

Use low-queue observations to estimate a propagation floor, without learning persistent queue inflation as acceptable delay. Aging a performance sample does not erase debt. Use monotonic local clocks; client/server need not share a wall clock for local RTT samples.

Compute BDP-based stream/session flight subject to global memory, RX credit, real grants and service capacity. Pacing and admission are coupled to queue/RTT budget, but a self-imposed pacing rate must not become the permanent capacity estimate. Probe additional capacity in bounded turns independent of that estimate. Report the reason when the rate is held back.

When ordinary application RTT/queue pressure rises, stop extra bulk admission promptly, shorten bulk assembly/service turns, retain urgent/control/interactive progress and sufficient cleanup. Never discard sealed or ambiguous work to make queues look smaller. A DATA feedback turn longer than 1000 ms does not redefine the application target.

Idle clears/ages only performance estimates and excess speculative admission. Resume/recovery starts with bounded fresh probes and an explicit ramp budget. Prevent the permanent low-speed state after one outage. Do not change account preference every noisy sample; use stable ties, confidence, freshness, hysteresis and a tested fallback when the preferred path loses grants/readers.

## 9. Lift the account-count limit completely across the product

This is a mandatory implementation item for 0.9.2-Rev1, not just a documentation change. No source limit is lifted by the planning document itself.

1. Remove `len(c.Accounts) >= 10` from server account addition and `len(c.Accounts) > 10` from configuration validation. Replace manager `/10` text and the add-account rejection with actual configured/enabled/ready counts and resource status.
2. Remove Android's arbitrary `1..64` account restriction and add-button-at-64 restriction if the intended product is unrestricted account count. Keep at least one valid/enabled account, unique IDs/credential refs, valid TLS/host/provider fields and bounded decoding. Do not simply substitute a new hidden ten-like cap.
3. Audit invitation export/import, server `Profile`, Kotlin vault/editor/migration, `NativeConfig`, native bridge, `StartClient`/`Serve`, account selection/readiness, diagnostics/export, UI lists and tests for truncation, fixed arrays or per-account channel/resource multiplication.
4. Retain profile/config input byte limits as input safety; make supported byte budgets explicit and consistent. Current server and native parsers have 1 MiB bounds. Oversized imports must produce a clear size/resource error before mutation; no silent missing accounts. Use file import when a many-account deep link is impractically large.
5. Preserve stable account IDs/tombstones, credential isolation and debt directories on remove/re-add. Current `a%02d` formatting expands beyond two digits; verify a100/a128 rather than assuming a two-digit schema. Do not renumber existing accounts.
6. Separate configured account count from concurrently active sessions. Activate accounts using bounded workers and aggregate fd/memory/ops/reconnect budgets; UI shows pending/degraded/reconnecting with reasons. Extra accounts can add independent capacity where measured; they cannot defeat a shared provider bottleneck.
7. Preserve the existing supported user limit unless separately requested. More accounts must not accidentally weaken per-user isolation or multiply provider socket allocations per user.

Required configuration round trips: 1, 2, 5, 10, **11**, 16, 32, 64, **65**, 128 accounts; import/export/edit/restart preserve all entries and secrets correctly. Include a100/a128 IDs, partial disabled sets, invalid/duplicate account rejection, transactional failure, and oversized-input diagnostics. Real socket/lifecycle stress uses feasible active subsets; the number of configured accounts is not permission to open thousands of sockets.

## 10. Replace the performance tests and qualification tooling

### 10.1 Why today's Go PASS is insufficient

Verified examples:

- Archived Rev10 full-core report: 718 passed test events, **12 optional skips**, including main throughput, startup, heterogeneous and application phase suites. That is a correctness/build report, not a field speed result.
- `TestSpeedtestMultiflowAcceptance` fixes ten accounts, defaults to **600 Mbps synthetic carrier**, 65 ms synthetic command delay and a **50 Mbps** target. A pass says nothing about an unmeasured live account's operation/visibility limits.
- Its throttle/outage/unstable branches explicitly set `ITOM_LOAD_SUPERVISORS=0`, replacing the public production supervisor path with raw-lane fixtures exactly where recovery behavior matters.
- `run_speedtest_gate.py` allows 4–32 streams; it cannot run a true SINGLE case. Some load helpers bound accounts to 1–10.
- `--suite full` runs default normal steady plus startup/heterogeneous/fragmented/application cases, but does not automatically run every degraded profile; `--all` is restricted to steady. The required matrix needs an explicit complete manifest.
- Steady load commonly writes 32 KiB chunks. Fragmented acceptance adds paced 1460-byte writes, which still do not reproduce the real native/TUN TCP ACK/windowing path.
- Local Go TCP/HTTPS fixtures cover encryption and IMAP, but most enter `Client.Open` directly and do not traverse actual Android VPN routing, gVisor TUN, protected socket callbacks, phone CPU/GC, or handover.
- `run_speedtest_gate.py` checks process exit status but does not itself enforce the expected named-test PASS/no-SKIP contract. Preserve and strengthen `rework_gate.py`'s exact-test/source-fingerprint checks.

Do not discard security/accounting/lifecycle tests because their throughput fixtures are unrealistic. Replace their performance assumptions and shared scenario infrastructure; retain independent assertions. Unit tests that feed a synthetic 100 Mbps counter to a controller are policy tests, not bandwidth measurements.

### 10.2 One scenario engine, separate evidence layers

Use one scenario description, workload generator, independent receiver verifier and result schema across:

| Layer | What it proves | What it cannot prove |
|---|---|---|
| Unit/property/fuzz/race | Envelope/parser/credit/order/journal transitions and concurrent reservation correctness | Provider or Android speed |
| Full public Go transport over real TLS IMAP sockets | Scheduler, crypto, journals, supervisors, control/RX/cleanup behavior under specified service model | Actual live account/device capacity |
| Native host packet path | gVisor/SOCKS/DNS/TCP fragmentation/ACK behavior and bridge contracts with production transport | Physical Android callbacks, routing, radio and CPU limits |
| Actual Android TUN + matched VPS + live IMAP | Requested real behavior, Speedtest startup/SINGLE/MULTI, sustained goodput and loaded RTT | Universal behavior on untested providers/networks |

Before transport tuning, run a direct baseline and calibrate raw account APPEND/FETCH/visibility/IDLE/cleanup, individually and concurrently, using bounded scratch namespaces and authenticated disposable test traffic. Do not purge production mail/debt. Credentials come through existing secure configuration, not chat. A direct phone→VPS throughput/RTT baseline distinguishes access/exit capacity; raw mailbox calibration identifies the additional carrier constraints.

Fit provider profiles to measured distributions, not just a fixed sleep per command. Include per-account and shared host/IP/provider byte and operation caps, aggregate connection limits, concurrent-command dependence, APPEND/FETCH latency tails, delayed visibility, folder backlog, GC cost, MIME transformations, lost IDLE notifications, idle disconnects, UID gaps/UIDVALIDITY change, explicit refusal, ambiguous APPEND, rate backoff and partial/all outages. Supply clean, ordinary aged and Rev10 retained-debt starting states.

Model TCP propagation/loss with appropriate network-level shaping or field execution. A stream relay dropping bytes inside reliable TCP is a protocol corruption experiment, not TCP packet loss. Wrapper timing replay is an explicitly limited stress model. Preserve real supervisor/reconnect/journal paths in every integration profile.

Calibrate against multiple held-out provider runs: compare command p50/p95/p99, visibility, app-byte trajectory, control/ops ratios and RTT. If the model cannot reproduce the field bottleneck/gap within a predeclared tolerance, label it uncalibrated and fix the model before using it for tuning. Freeze parameters before candidate runs; never fit a model just to make the new transport look good.

### 10.3 Workload and measurement contract

- SINGLE, 4/8/16/24/32 streams per direction; download-only, upload-only, sequential DL→UL, reverse order and duplex. Include equal-account and heterogeneous-account conditions with matched total physical capacity.
- Cold DNS/TCP/OPEN/TLS/HTTP starts, 1/8/24/48 concurrent connections, small configuration exchanges, bursty HTTPS/HTTP2 requests, persistent sockets, half-close, cancellation, slow consumers and mixed interactive/bulk. Use actual protocol workload when available; call the local substitute Speedtest-like, not an Ookla reimplementation.
- Generate deterministic noncompressible streaming payloads and independently verify receiver integrity. Count bytes at each receiving application, timestamps into immutable monotonic windows; server receipt must confirm uploads. Keep source generation/copy counters separate.
- For lab capacity runs: at least 10 s warm-up plus **60 full measured seconds**, three consecutive runs with the same logical transport and durable state; publish full interval, final 30 s, one-second windows and every rolling ten-second interval. No post-deadline bytes or warm-up bytes count. Preserve scheduled probes until their outcomes arrive.
- Also run at least three ten-minute healthy-load measurements and a **60-minute** mixed load/idle/recovery/debt soak with bounded resource tracking. A short burst cannot prove sustained reclaim.
- Application probes at one per second and bounded additional burst samples, classified as ordinary comparable traffic. Report every scheduled sample, including delayed launches, missing, timeout, corruption and cancellation. Use one monotonic round-trip clock; tiny transport PING is a separate metric. Include ordinary unprioritized/bulk-path response measurements.
- After five minutes idle, repeat cold starts, SINGLE/MULTI and load on the same state. Repeat Wi-Fi/mobile transitions and partial account reconnections without resetting journals.
- Do not impose an artificial 60-second measurement on a proprietary Speedtest app that uses its own duration: record its raw stages/durations/results, and supplement with the fixed-window app workload. App startup failures still count.
- Record source/build identity, profile/seed, accounts configured/enabled/eligible, provider model/caps, workload, topology/shards, actual literal encoding, CPU quota/GOMAXPROCS, mem/fd/GC/fsync, diagnostics overhead and exact outcomes.

### 10.4 SINGLE versus MULTI investigation

Run interleaved randomized SINGLE/MULTI pairs against the same endpoint and account pool, then repeat with reversed order. Compare median full-window goodput, tail windows, startup duration, ordinary RTT and control/operation/physical-byte cost. Use at least six paired runs per key condition and report dispersion/confidence, not one pair.

Inspect whether more streams create more APPENDs per useful MiB, weaker batch fill, excess OPEN/STATUS/GC, preparation/lock contention, logical-owner fairness throttles, memory/GC/fsync pressure, or cross-account reordering. Profile actual Android and VPS CPU before attributing all loss to IMAP.

Do not demand MULTI beat SINGLE on every trial: normal measurement variance exists. Set a healthy capacity-matched **initial investigation threshold** of a repeatable >10% median MULTI regression; any such result needs a documented bottleneck and correction before release. Freeze that threshold and uncertainty treatment before testing; do not relax it after seeing a failure. Report useful per-stream fairness and saturated aggregate capacity rather than multiplying peaks.

### 10.5 Harness enforcement

Create a versioned required-case manifest. Parse `go test -json` and application result JSON, require each named test to start and finish PASS, reject missing/no-match/SKIP/FAIL, partial durations, wrong source hashes, stale reports, changed profiles and missing denominators. Explicitly mark `PASS`, `FAIL`, `NOT RUN`, or `BLOCKED`; never turn missing credentials/device into PASS.

Run performance without race instrumentation, builds or other load concurrently. Run separate correctness/race checks. Reuse fixtures and assertions, not an implementation-mirroring test suite that asserts the same formula from both sides. Record all runs, including failures and initial dirty-state failures. Diagnostic-disabled success must reproduce with bounded normal telemetry; trace cost is measured separately.

## 11. Qualification gates for 0.9.2-Rev1

The 100/100 claim requires the real field gate. Calibrated lab success is necessary but insufficient. Freeze the scenario matrix, thresholds, exception classification and feasible-capacity assumptions before candidate measurements.

| Gate | Required outcome |
|---|---|
| Provenance/build | Hash-bound baseline/source, correct new identity, modules locked, required tests actually executed; server+APK native libraries match candidate |
| Correctness | Byte integrity, authentication/replay/isolation, order/half-close/RESET, exact debt/proof and crash/restart/migration regressions pass |
| More than ten accounts | All listed config round trips including 11/65/128 pass; resource-bounded activation/import/native/UI/metrics work without truncation |
| Healthy sequential and duplex speed | For adequately provisioned calibrated lab and matched field fixed-window workloads: >=100 Mbps each active direction over every full measured interval, final 30 s, and complete rolling ten-second window; three consecutive 60 s runs and three ten-minute runs |
| Loaded/idle RTT | Every scheduled healthy application RTT strictly <1000 ms, no missing/timeouts/corruption; report distributions/jitter/overhead. A mean or p95 alone cannot pass an outlier. Cold connection completion has a separate startup gate |
| Startup | Zero failed initiations across at least 100 scripted cold launches spread over concurrency conditions; proposed healthy p95 TCP/OPEN <=2 s and TLS/config-ready <=5 s, maxima <=5/10 s respectively. Freeze stricter/provider-specific feasible budgets before candidate runs. Repeated real Speedtest startup succeeds in both modes |
| SINGLE/MULTI | SINGLE can borrow adequate BDP and pool capacity; no unexplained repeatable >10% healthy matched MULTI median regression; cold and established results separately reported |
| Control/reclaim | Initial <1 standalone control APPEND/DATA APPEND healthy-bulk objective or a measured justified protocol cost; full ops/byte budgets met; reclaim keeps up; no increasing settled debt/backlog/control slope over steady soak |
| Asymmetric/degraded | 5/12/25/50 Mbps and unequal accounts/directions, shared operation caps, delayed visibility: safe bounded admission, interactive/fair progress, RTT ceiling where physical floor permits; reduced speed clearly capacity-limited, never a 100/100 PASS |
| Partial faults | Any account/control-preferred account unavailable, reader-only or writer-only failure, quota exhaustion, lost notification, lease rollover: remaining usable capacity progresses; no global stall/deadlock or unsafe refund |
| Outage/recovery | One/multiple/all-account outages at 2.5/10/30 s; ambiguous outcomes preserved. Initial recovery budget: first useful progress <=5 s and >=90% of matched pre-fault feasible goodput <=30 s after access/account service is healthy again, unless a calibrated mandatory provider backoff requires a predeclared different case |
| Idle/soak/resources | Five-minute idle/resume plus 60-minute mixed soak; no persistent collapse, unbounded queues, debt leaks, fd/socket/goroutine growth, runaway polling, or credential/payload trace leakage |
| Android/live provider | Exact APK/server pairing; real TUN/DNS/protected sockets and native TCP path; direct baseline documented; three or more repeated SINGLE and MULTI Speedtests plus fixed-window sustained load, idle and handovers |
| Upgrade/rollback | Consistent stopped-state backup, bounded non-destructive migration, old-debt/legacy-folder drain, atomic install, real bidirectional functional health, tested failure rollback |

Outages or intrinsic RTT >=1000 ms make the absolute healthy goal impossible during those intervals. Report them as violated/outage intervals with elapsed deadlines and recovery results; do not delete samples. Bounded safe behavior can pass the outage contract while the absolute RTT goal remains failed for that interval. A stable capacity-limited build may be useful but is not a qualified 100/100 release.

Thirty/fifty Mbps milestones are internal diagnostic checkpoints. No alternate threshold, tolerance, or easiest seed may replace the above final speed gate. Resource assumptions and calibrated physical ceilings belong alongside every result.

## 12. Persistent state, compatibility, and security

Retain these independent tests/contracts even if all transport code around them changes:

- Authenticated account/user/session/generation/owner/attempt and message binding, Noise/AEAD counters, anti-replay, bounded lengths, routing/address policy and registration controls.
- Unknown APPEND remains charged until authoritative reconciliation/proof. Definitively unstarted work may return its reservation only through the correct durable transition. New logical repair does not cancel prior physical uncertainty.
- Canonical MIME charge is stable under allowed provider CRLF/folding/base64 rewrites. Actual received physical bytes/UID/UIDVALIDITY remain immutable cleanup evidence. Ciphertext/digest matches alone are not proof of absence/deletion.
- Exact-set deletion, partial EXPUNGE, UIDPLUS/non-UIDPLUS fallback, UIDVALIDITY change, malformed/oversize MIME, mixed invalid/valid proof isolation, bounded deterministic quarantine and auditability.
- Logical ACK/STATUS, RX memory credit, actual application consumption, physical service slots and durable byte/message debt stay separate. None is an interchangeable refund token.
- Dirty wallet/history-missing/retained-grant recovery, every crash boundary around prepare/start/spend/APPEND/result/UID/receipt/delete/refund, and replay across restart.
- Slow consumer, duplicate/reordered members, cross-user grants, cancellation after sealing, half-close/FIN/RESET, carrier replacement and total outage behavior.

Version the new wire/envelope/layout explicitly and negotiate required capabilities. Keep existing persistent formats where possible; if new layout/member state needs a new schema, write an idempotent bounded migration with pre/post audit, consistent backup and restore tests. Migration records old unresolved grants/attempts rather than manufacturing spends or releases from absent history.

New and legacy folders can coexist during drain under one physical-account budget. Do not delete `/var/lib/itom`, databases, unknown mail or user profiles to achieve readiness. Keep control/routing authorization for cross-account messages bound to original scope. Normal account retirement retains tombstones and historical debt.

Plan a coordinated client/server upgrade: old peers reject unsupported new publication safely with a clear compatibility error; old DATA can drain through a compatibility reader without enabling mixed-version publication. A live switched topology needs a durable fence against old/new double admission. Test interrupted upgrade and remote messages created between backup and rollback; a state snapshot alone cannot undo remote mailbox effects.

Preserve Android app label **ITOM**, supported UI/manager features, English UI, diagnostics, routing/network policy, SOCKS credentials, package identity `org.itom.client.dev.rev7` and signing continuity unless an explicit tested migration requires a change. Increase `versionCode` above 9089; choose the project-appropriate value during implementation. Use `0.9.2-Rev1` consistently in version names, filenames, build reports and source metadata, with proposed internal revision `transport-rework3-rev1`. Protocol capability version is a separately reviewed compatibility decision, not just a renamed marketing string.

## 13. One-session implementation sequence

One pass means one coherent final release candidate, not coding blindly without experiments or compiling only at the end. Internal checkpoints, comparison runs and corrections remain mandatory; do not issue a string of public Rev11/Rev12 tuning releases.

| Order | Work | Completion checkpoint |
|---:|---|---|
| 1 | Verify exact Rev10 source, archive manifests and prerequisites; create isolated working checkout; preserve baseline and dirty fixtures | Baseline identity and runnable required-test inventory recorded |
| 2 | Add receiver-window metrics, critical-path tracing, physical ledger report, topology/resource model and direct/raw-provider calibration interfaces | Stage counters have explicit semantics and unknown intervals; no duplicated bytes or unsafe ledger inference |
| 3 | Replace scenario engine, mandatory manifest, SINGLE/MULTI/packet-path coverage and calibrated provider models | Existing field gap is represented or explicitly unresolved; mandatory test execution enforced |
| 4 | Remove server/Android account-count caps, refactor bounded activation/configuration/diagnostics | 11/65/128 account round trips and resource stress pass |
| 5 | Specify wire/envelope, stream ownership, RX credit and quota-authority adapters; implement compatibility/drain/migration | Security/debt/crash tests pass before load claims |
| 6 | Implement account/folder service manager, atomic reservation, directional fairness, topology and session budgets | No oversubscription/lost wakes/deadlocks; same semantics both directions |
| 7 | Implement DATA batching/striping/receive/reorder and independent application delivery | Integrity, slow consumers, half-close and missing-stripe repair pass; SINGLE not hard-capped by fixed 3 MiB |
| 8 | Implement cumulative/piggyback control, descriptor-free verified new envelope, cleanup/receipt batching and liveness | Control efficiency improves under real budgets; no debt/refund shortcut |
| 9 | Implement the one RTT/capacity policy, sample aging, recovery and controlled exploration; remove superseded production policies | Calibrated healthy/degraded/idle/fault scenarios meet their contracts |
| 10 | Compare topologies and account/stream/shard sweeps; choose provider-specific defaults from evidence | Decision report retains failures, costs, sample uncertainty and exact frozen settings |
| 11 | Run complete core/native/vendored/Android/domain/lint/build and relevant race/fuzz/migration checks; then serial performance/soak | All required cases run; failures fixed; no synthetic-only release claim |
| 12 | Produce matched candidate artifacts and execute actual device/provider qualification plus representative update/rollback test | Honest gate report states exactly what is qualified, capacity-limited or blocked |

Do useful local implementation while real-device credentials/runner setup is pending. Stop dependent field qualification when its required inputs are absent; do not replace it with a PASS label. If external capacity disproves a topology assumption, adjust that implementation within the same pass and retain the rejected experiment's evidence.

### 13.1 Required diagnostics for bottleneck isolation

For each local stage emit duration/queue/service/result on a bounded pseudonymous identity. Trace ingress → stream flight → assembly → account eligibility → service/lease reservation → journal/spend → APPEND → visibility → notification/FETCH → authentication → reorder → receiver/application → progress → cleanup proof/refund. Include source/type/age/confidence for feedback samples and the explicit reason work waits.

The physical ledger report partitions configured capacity, reserved grants, charged attempts/uncertainty, settled proof and truly allocatable bytes/messages, per account/class/user/profile. Explain historical unknown records without exposing secrets. Measure diagnostics cost; sample expensive detail and keep counter/event queues bounded. Cross-host joining records clock-alignment uncertainty and incomplete rotation coverage.

Compare in this order to isolate the gap: direct network/application → raw mailbox service → full Go public transport → native packet path → phone TUN/live provider. If raw account service is fast but the Go path is slow, inspect scheduling/feedback/credit; if Go is fast and packet/phone is slow, inspect native TCP/TUN, CPU and workload mismatch. Retain both directional evidence rather than optimizing upload while concealing download.

## 14. Fresh verification performed for this plan

These are checks of the existing baseline and analysis, not the replacement or field target:

| Check | Result |
|---|---|
| Outer archive checksums and safe path inspection | 188/188 listed files match; no unsafe extracted path |
| Nested current source-manifest checks | 654/654 match |
| Independent raw-trace parsing and archived analyzer rerun | Counts/coverage/core duration metrics reproduced; independent successful-only, per-account and overlap results added |
| Four screenshot comparisons | Values match supplied table |
| Existing Go `servercfg`, `wire`, `wallet`, `journal`, `flow` suites | PASS, cached dependencies, Go 1.27.1 |
| Existing `mailbox` suite | PASS, 2.155 s package result with local test sockets allowed |
| `TestField176RetainedMessagesPreserveDebtAndBootstrap` | PASS including retained-slot boundaries |
| `TestField176RetainedMessagesTLSAllAccounts` | PASS, 3.320 s package result; ten accounts/twenty directions with TCP echo, old grants intact and journal audit |
| `TestDeliveryFloorCannotLearnStalledFullWindowOrRepair` | PASS; policy regression, not performance evidence |
| `TestSpeedtest50MbpsRTTGate` | PASS; validator rejects outlier/missing/tail failures, not a bandwidth run |
| `TestCapacityBroadcastWakesAllSpeedtestWaiters`, `TestSharedAccountAdmissionUnderSpeedtestLoad` with race instrumentation | PASS |
| Existing Python tools/launcher unit tests | 93 PASS; mocks do not establish an APK build |
| Full Go/native/Android/race suites, calibrated throughput, live provider, actual phone, VPS deployment | NOT RUN in this planning session |

The first mailbox check failed before behavior testing because the default sandbox denied local listener creation. Rerunning that suite with loopback-capable execution passed; preserve the setup failure separately. An initially mistyped TLS-test name did not execute that case; the exact declared name above was subsequently selected and completed. Toolchain version was observed locally; no new official distribution download/checksum attestation was performed. The next release build must verify the toolchain distribution and modules independently.

No transport source or release artifacts were rewritten for this plan. Recheck the manifests after analysis to distinguish generated test caches from hashed-source changes.

## 15. Next-session handoff and final deliverables

Bring **this plan and the original uploaded Rev10 handoff ZIP** to the implementation session. Do not assume `/workspace` or this conversation's local files will transfer. Recover the baseline from `source/current/ITOM-0.9.1-Rev10-sources.zip`, verify the hashes in section 2, and treat old handoff task instructions as historical evidence.

Needed for dependent field work: securely configured provider test accounts and their actual constraints, representative phone/device runner, matched VPS staging access, installed capacity/debt profiles and signing identity, direct network baselines and fixed workload endpoints. None of those capacities should be invented from the old 130 Mbps note. Keep credentials and signing material out of chat and deliverables.

Suggested next-session instruction:

> Implement ITOM_0.9.2-Rev1_TRANSPORT_REWORK_PLAN.md as one coordinated replacement of the transport and performance harness, using the hash-verified 0.9.1-Rev10 source from the attached handoff. Lift the account-count restrictions throughout server/Android/native/configuration. Implement equal-capability pooled accounts, protected control/interactive service, bounded provider-specific folder topology, symmetric publication/receive orchestration, authenticated self-describing DATA, independent logical/physical credit, adaptive BDP and queue/RTT control, bounded recovery, and realistic SINGLE/MULTI/native/field measurement. Preserve old state/debt/security and implement explicit compatibility/drain/migration. Work through the internal checkpoints, compare topologies under calibrated resources, and produce one 0.9.2-Rev1 candidate with honest mandatory-gate evidence. Do not claim 100/100 or field qualification from Go tests alone, and do not reset journals/mailboxes to obtain a pass.

Final implementation deliverables: source archive/commit and SHA-256, selected architecture/defaults and rejected alternatives, root-cause evidence with observed/inferred labels, migration/compatibility/rollback notes, exact required-case result JSON and logs, matched Linux amd64/arm64 binaries and Android native artifacts for arm64-v8a/armeabi-v7a as supported by the existing workflow, signing/package/build identities, sustained field results and explicit limits.

Retain the useful recovered workflow preference for one reproducible build command and one manual VPS-update command after staging the binary in `/tmp`. Derive and test their actual syntax from the completed candidate; do not invent runnable Rev10 installer commands in this plan. The updater must verify architecture/hash, stop before a consistent restricted backup, preserve config/state/custom service settings, install atomically, verify useful bidirectional health and exercise rollback. Publishing or updating the live VPS is a separate action from producing a reviewable candidate.

The plan is complete when its evidence, tradeoffs, capacity model, interfaces, account-limit changes, test replacement, implementation sequence and qualification gates are reviewable. The implementation is complete only when those gates have real outcomes; missing field access remains explicitly blocked, not success.
