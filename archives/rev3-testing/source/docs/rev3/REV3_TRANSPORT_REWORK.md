# ITOM 0.9.2-Rev3 transport rework

Rev2 improved SINGLE but MULTI could stall and inflate loaded RTT. Rev3 keeps a shared authenticated logical session and replaces the scheduling decisions that let unrelated TCP streams block each other. The real-provider goal remains useful 100/100 Mbps sustained with every loaded application RTT below 1000 ms. Results and limits are in `REV3_FIELD_FINDINGS_AND_RESULTS.md`.

## Implemented changes

- Interleave stream prefixes in the engine and batch admission. Preserve offset order within each stream and rotate scarce credit. A stopped batch member yields without rejecting live siblings.
- Sort stream keys and group item IDs instead of sorting the entire pending set through repeated map lookups. A MULTI CPU profile exposed the latter as a major cost.
- Bound speculative bulk bytes to actual admitted flight plus eight physical batch targets, within the existing 32 MiB hard logical limit. Unused learned window capacity does not allocate a large actor backlog. Soft stream shares prevent one delayed stream consuming most flight. Advisory queue headroom never fragments an otherwise complete TCP read.
- Preserve 128 short-stream item and attempt slots. Bound total attempt metadata at 16384; physical grants and journals remain separate authoritative limits. An exhausted repair or account reservation skips that item instead of ending the scheduling pass.
- Return unadmitted batches to the actor with a predicate wake. Physical workers remain available to publish already-charged prefixes and repairs. Recheck retry predicates to avoid lost wakes.
- Retain the same authenticated, unspent NACK permit when a repair is rejected before APPEND. Such a deferral cannot spend another physical retry or wait for a new NACK. Committed and unknown publications retain the original bounded retry budget and physical debt.
- Bound accepted BODY visibility grace to 2–3 seconds, calibrated from acknowledged low-flight samples. A hole's own growing age cannot extend its repair delay.
- Use the byte-weighted p90 outstanding age while useful bulk progress is fresh. Collective loss still uses the oldest range and the original outage guard. Repair ACKs count useful bytes exactly once but do not feed old scheduling residence into fresh congestion samples. Bulk/fast delay objectives remain 800/650 ms.
- Separate control service cost from occupancy on independent DATA sockets. Keep per-stream feedback affinity, coalesce cumulative bulk feedback for up to 20 ms, and shorten that wait when urgent control arrives. Rendezvous routing mixes the whole stream ID and uses responsive accounts, avoiding paired upload/download IDs selecting only a subset of paths. Full or disconnected paths retain fallback.
- Add actor-owned scheduler and stream-frontier snapshots, oldest bulk range age, and bounded envelope receive/delivery diagnostics. Observations expose metadata, never plaintext application payload or credentials.

## Account and folder options

| Scheme | Benefits | Costs and limits | Rev3 decision |
|---|---|---|---|
| Two folders per account, own TCP streams and control/DATA | Few provider sessions and simple mailbox management | Shared BODY/control service can block short requests; a stream inherits its account's slow path; spare accounts cannot readily repair it | Preserve negotiated mixed fallback; shared logical stream ownership supports sibling failover |
| Separate control folders and additional DATA folders; equal accounts | Independent DATA work preserves control access; accounts remain interchangeable | More sockets and durable folder scopes; provider connection limits still apply | Default negotiated split layout, one DATA folder per direction; optional static two/four shards |
| Dedicated DATA accounts plus a small CONTROL group | Concentrates feedback, can isolate heavy DATA readers | Small control capacity becomes an aggregate bottleneck; lost control accounts affect many DATA streams; quota controls must still use their original physical account | Avoid permanent roles or a fixed small feedback group |
| Hybrid adaptive routing | Stable stream affinity with responsive fallback; independent DATA and control costs; uses spare account capacity | Requires bounded queues, authenticated routing, and visibility of both logical and physical debt | Rev3 uses this scheme over equal-purpose accounts |

Dynamic folder creation/deletion is not used as a response to each load change. UID discovery, durable scope and cleanup are harder to reason about while folders churn. Static negotiated isolation plus adaptive scheduling addresses service contention without changing quota authority.

## Accounts, safety and upgrade

The maximum of ten configured accounts remains removed. The serialized profile/configuration limit is 1 MiB. Active endpoint connections, provider sessions, login parallelism, actor memory, attempt metadata and physical journal capacity are independent bounded resources. Tests covering 128 accounts are a tested range, not a new profile account ceiling.

Authenticated stream controls can use sibling accounts in the same user/session/generation. Durable grant, receipt, cleanup and retirement authority stays bound to its original account. Queue decisions never refund unknown APPENDs. Preserve installed journals and wallets. The wire envelope and handshake capability formats remain compatible with the Rev2 architecture; use both Rev3 endpoints when measuring this rework.

## Verification

Negative regression runs demonstrate the original scheduling, parity collision, repair-permit, self-extending grace and queue-sizing defects. Each major change has a targeted Go run, followed by final normal/race/build checks. Fresh production supervisor tests use TLS/IMAP and independently verified useful TCP byte patterns. Qualification requires sustained and final-tail 100/100 Mbps, every loaded probe below 1000 ms, no timeout/missed probe and useful traffic overlap in both directions. Missing, skipped and partial results fail. Laboratory models do not establish Android/provider performance.
