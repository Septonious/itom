# transport-s1: S0 contract and first end-to-end S1 transport slice

## Status

This revision starts the architectural transport replacement on the production Android -> IMAP -> VPS path. It is not a field-performance claim and it does not mark all of S1-S5 complete.

Implemented in this revision:

- authenticated carrier attachment with exact `DATA_BATCH | STRIPED_DATA | LOGICAL_CONTROL` capabilities; the current source uses `ITH9` / wire version 9 and has no older-wire downgrade;
- one logical-session identifier and fresh generation shared by all account carriers of one Android `StartClient` instance; reconnects advance a per-account carrier epoch;
- logical session/generation/carrier epoch/capabilities are bound into the Noise prologue through `ConfigHash`; stale carrier callbacks cannot authenticate as the fresh carrier;
- bounded multi-record DATA batches: current hard limit 16 encrypted records / 576 KiB member payload; bulk target 320 KiB with a fixed 2 ms residence deadline, plus the perf2/perf3 reserved short-DATA class documented below;
- one physical APPEND and one durable physical outcome per batch; all member logical attempts keep their individual flow state and are released only after physical cleanup proof;
- exact MIME-size check with a 512 KiB hard cap before APPEND;
- C2S receive admission understands the larger ITD3 physical envelope instead of the legacy single-record `MaxRecord` cap; unmatched physical DATA is still bounded by an 8 MiB MIME holding budget while waiting for its authenticated UsageReport;
- client C2S batches retain the existing durable wallet spend/fence/UsageReport rules; server S2C batches retain durable Prepare -> Start -> APPEND -> Accepted/Uncertain ordering;
- temporary S1 credit contract is explicitly one grant per batch (512 KiB / one message). Sixteen live grants preserve the existing 8 MiB journal budget; two client-side permits are reserved from bulk use for interactive DATA. This is replaced by cumulative leases in S3;
- VPS `AccountRuntime` owns one bounded physical APPEND writer per real IMAP account and is shared by all enabled users of that account. Per-user read/recovery state remains isolated by folders;
- Android UI and routing configuration are unchanged. The native Android backend already calls `tunnel.StartClient`, therefore this transport is the path used by the VPN rather than a separate CLI prototype.

Not yet claimed complete:

- the first carrier-independent DATA striping slice is implemented: a stream keeps one owner Pump/control carrier while physical DATA can use other healthy carriers in the same authenticated LogicalSession. Full server-side `UserSession` ownership above all Pumps, server-owned generation arbitration, and seamless stream survival when the owner carrier disappears are still not complete; fix5 adds fresh authenticated boot challenges but `Generation` is still client-created;
- reader HOT/WARM/COLD/IDLE work is S2;
- cumulative leases and grouped physical accounting are S3;
- S4 is now partially implemented by bounded cross-account DATA striping. Adaptive DRR/flowlet weighting, carrier-quality scoring and loss of the owner carrier without stream loss remain;
- no real Rambler throughput/loaded-RTT result is claimed until the user's field matrix is run three times.

## S0 state contract

`LogicalSession` is created once per Android `StartClient`. In this S1 slice, `Generation` is a fresh non-zero client-created generation nonce for that process-level logical session; it is authenticated by the Noise binding, but server-owned generation/challenge arbitration is not implemented yet. `CarrierEpoch` is non-zero and increases on every reconnect attempt for one account. A carrier is accepted only if all three values plus exact capabilities reproduce the Noise binding and the Noise handshake authenticates with the profile PSK.

`ITD3` is an outer framing container only. Every member remains a normal independently authenticated Noise `Record`, including its own nonce/counter/replay state. A batch may contain different TCP streams but only one pair/session/epoch/direction. The receiver validates the full bounded framing before consuming any Noise replay state.

Logical attempts remain per DATA record in `flow.Tx`. Physical accounting is per mail: one S2C journal attempt or one C2S wallet descriptor covers the whole batch. The mapping from that physical proof to all member logical attempt IDs is retained until receiver-owned cleanup confirms release. `Unknown` after APPEND start remains debt; it is never converted into free capacity by timeout, cancellation, logical receipt, or reconnect.

Each batch deadline is fixed from its first record and is never extended by later records. Queue, record count and byte caps are finite. MIME is encoded and measured before APPEND, and anything above the current 512 KiB hard cap is rejected locally.

## Compatibility

Wire/handshake version is currently `ITH9` / version 9, capability set is exactly `DATA_BATCH | STRIPED_DATA`. `ITH6`, `ITH5`, fix3/android-4 and current peers do not mix, and there is no automatic fallback. Android and VPS must be updated together. Existing configuration/profile data is retained; live TCP sessions are not promised across incompatible transport updates.

## Verification in source tree

Added unit coverage for ITD3 framing/context bounds, the larger bounded C2S physical envelope, and Pump coalescing work from multiple streams under the fixed deadline. Existing end-to-end TLS-IMAP/TCP tests exercise the same `WriteBatch` method because `lane` implements `BatchLink`.

Offline source/launcher gates remain mandatory. The full pinned Go 1.27.1 race/vet/test gate and Android build remain build-time gates; a source release must not claim them if the environment did not actually execute them.

## Next S1 work

Promote the current LogicalSession-wide stream registry into a full per-user owner that survives loss of the current control/owner carrier, then finish server-owned generation arbitration (fresh server challenges are already enforced in fix5) and carrier-quality-aware DRR/flowlet weighting. Validate HTTP, TLS upload/download, server-first, half-close, two simultaneous users over the same ten accounts, a failed non-owner carrier, and finally owner-carrier loss before proceeding to the remaining S2/S3 work.

## transport-s1-perf1 performance architecture

Field testing of fix3 still showed about 5 Mbps with DATA RTT rising into multiple seconds under load. This revision removes the serialized-IMAP bottlenecks rather than increasing timeouts:

- control, receive polling and DATA APPEND no longer share one physical IMAP command connection after activation;
- each Android account lane gets a dedicated reader and up to two bounded DATA APPEND connections, with safe fallback when the provider refuses extra connections;
- the VPS keeps one shared per-account DATA runtime for both users, now with two physical APPEND workers and per-user round-robin queues;
- Pump permits only the physical APPEND stage to run in parallel; flow state/completions remain serialized by the actor and wallet/journal durability remains mutex-serialized;
- ITD3 grows to at most 16 records / 576 KiB decoded payload, with a 1 MiB MIME hard cap; the total unmatched/pending physical exposure remains capped at 8 MiB;
- the stream window rises to 2 MiB and per-lane logical/account admission to 4 MiB so the IMAP bandwidth-delay product is no longer constrained by the former 128 KiB window;
- active receive polling is demand-adaptive (5-35 ms while active, bounded backoff while idle) instead of coupling receive progress to the writer/control command path.

This is still not the final cross-account UserSession/DRR striping design: one TCP stream remains owned by one account lane in this revision. The purpose of perf1 is to remove the much more immediate one-APPEND-at-a-time and shared-command-connection bottlenecks before moving stream ownership above carriers.

Memory/delivery invariants are unchanged: durable-before-APPEND, conservative Unknown after APPEND start, authenticated UsageReport matching, replay protection, bounded queues and physical cleanup before logical release remain mandatory.

`transport-s1-perf1` bumps the carrier handshake to `ITH6` / version 6 because its larger ITD3 bounds are not safe to negotiate implicitly with fix3 peers. Mixed fix3/perf1 peers are rejected during handshake.


### transport-s1-perf1-fix1 lifecycle correction

The shared per-account DATA runtime now binds each admitted physical APPEND to both the caller/lane lifetime and the runtime lifetime. Runtime shutdown therefore cancels in-flight IMAP commands even when a caller context remains live; a started mailbox APPEND still maps cancellation to the conservative unknown-outcome path. The bounded-runtime test transport now treats worker-entry signalling as observation only, so repeated requests cannot deadlock on the test probe itself.


## transport-s1-perf2 loaded-latency architecture

The perf1 full core gate exposed the actual loaded-RTT failure instead of a build/lifecycle problem: with an 80 ms synthetic IMAP command delay, probe traffic rose into multi-second latency while a bulk stream was active. The acceptance thresholds in `TestLoadedApplicationRTT` and `TestLoadedApplicationUploadWithTrace` are intentionally unchanged.

Perf2 removes two queueing sources without increasing timeouts, logical memory, or physical uncertainty budgets:

- Pump DATA admission is split into a reserved short-record queue and a bulk queue. Records up to 8 KiB receive a bounded low-latency path (up to 4 records / 64 KiB / 250 us coalescing), while bulk retains up to 16 records / 448 KiB / 2 ms coalescing. Four short batches form the fairness burst; an available bulk job is then serviced so interactive traffic cannot starve sustained transfer. The two queues together preserve the previous bounded job capacity rather than creating an unbounded priority queue.
- A short record arriving while a bulk aggregation window is still open terminates that collection window and rides the current physical APPEND. If all physical writers are already inside provider APPEND calls, it waits only for a bounded in-flight writer rather than behind the already-admitted bulk backlog.
- Usage/grant/open/liveness controls use the urgent control lane with a 1 ms coalescing window and are selected ahead of ordinary WINDOW/RECEIPT bursts. This is especially important for C2S `UsageReport`, because encrypted DATA remains deliberately unusable until that authenticated physical descriptor arrives.
- The dedicated receive connection now keeps its mailbox selected. `PageTail` performs one initial/periodic `SELECT` to establish/refresh UIDVALIDITY, then polls the next bounded 16-UID range directly with `UID FETCH`. Empty fast polls never advance the cursor, and UIDVALIDITY is refreshed every two seconds, so eliminating repeated SELECT commands does not weaken mailbox-incarnation checks or permit unbounded SEARCH.
- Active-reader delays are 0 ms while draining returned/advanced UID ranges, 2 ms while latency-sensitive control is hot, and 5 ms with active TCP streams; the existing bounded idle backoff remains.

New regressions verify that short DATA bypasses a queued bulk backlog and that tail polling receives newly appended mail without another SELECT between validity checks. Physical APPEND accounting, Noise/replay checks, durable-before-APPEND, conservative Unknown handling, 8 MiB pending physical budget, and per-message cleanup proofs are unchanged.

Perf2 does not claim cross-account striping of one TCP stream. A logical TCP stream is still owned by one account lane; if field testing after this scheduler/parallel-I/O revision still shows a hard aggregate ceiling, the next transport step remains moving stream/Engine ownership above carriers and DRR/flowlet striping across the healthy account pool rather than increasing queues or timers.


## transport-s1-perf3 physical-admission QoS

Perf2 proved that scheduler priority alone is insufficient: the bidirectional loaded test improved enough to pass, but upload-only probes could still wait 2.6-4.4 seconds even though their logical records were in the fast queue. The remaining queue was the physical storage-grant window: bulk DATA could consume every live C2S grant, forcing an interactive record to wait for remote physical GC before it was even permitted to APPEND.

Perf3 moves latency protection into the physical admission layer without increasing the uncertainty/memory budget:

- the total C2S remote-grant exposure remains exactly 8 MiB, but each grant is 512 KiB instead of the previous 1 MiB physical batch allowance, yielding sixteen live one-message permits rather than eight;
- bulk target batching is reduced to 320 KiB so Noise + ITD3 + ITQ1 + MIME expansion stays inside the 512 KiB permit; this still amortizes one IMAP APPEND across roughly ten full-size 32 KiB DATA records;
- two of the sixteen received permits are kept in a dedicated fast-grant reserve. Bulk DATA cannot borrow them. Short DATA may use a reserved permit first and may borrow an ordinary bulk permit when available;
- short DATA is no longer inserted into a partially collected bulk batch. A newly queued short record terminates bulk coalescing while remaining in the fast queue, allowing another physical writer to pair it with the reserved permit;
- grant wait tracing now records whether the batch was `fast` or `bulk`, making field traces distinguish scheduler delay from physical-quota delay.

The journal limit remains 8 MiB / 128 messages, pending unmatched physical DATA remains capped at 8 MiB MIME, and every grant still authorizes at most one physical message. No timeout, replay, durability, Unknown-outcome, or cleanup rule is relaxed. Perf3 also tightens the synthetic 80 ms loaded-RTT gate from 3s median / 5s max to 1.5s median / 3s max, so the build cannot pass by merely reproducing the old multi-second bufferbloat.

Perf3 still does not claim the S4 cross-account UserSession owner: one TCP stream remains attached to one account lane. If the real Rambler field run after this revision still exhibits a hard per-stream ceiling, the next slice is the carrier-independent shared Engine/stream registry and cross-account DRR/flowlet striping, not another queue/window increase.

## transport-s1-perf4 first multi-carrier DATA striping slice

Perf3 reduced synthetic loaded latency substantially, but it also demonstrated the architectural ceiling of a stream whose DATA remained physically bound to one IMAP account. Under the 80 ms command-delay test, bidirectional p50 reached roughly 1.39 s and upload-only max fell below 3 s, yet the design still could not combine the throughput of independent account carriers. Perf4 therefore stops tuning a single lane and moves physical DATA scheduling above carriers.

The vertical slice deliberately keeps control ownership narrow while making DATA carrier-independent:

- every Android `StartClient` creates one authenticated stripe group for its `LogicalSession`/`Generation`; the VPS creates the matching group per user after each carrier independently completes ITH7 + Noise authentication;
- stream IDs are allocated from one odd LogicalSession-wide space, so DATA arriving on a non-owner account can be routed unambiguously to the Pump that owns the TCP socket and flow state;
- OPEN, CONNECT_RESULT, WINDOW/CREDIT, FIN/RESET and socket lifecycle stay on the owner carrier. Only DATA batches are striped. This avoids duplicating or weakening the already-tested control state machine while removing the per-account throughput ceiling from payload transport;
- each physical batch acquires a slot on one healthy carrier using bounded round-robin selection. A carrier still exposes only its real DATA APPEND concurrency (normally two); one owner Pump may have at most eight physical DATA workers across the group. No byte/window/grant limit is increased by the extra goroutines;
- the selected target carrier seals every DATA record with that carrier's Noise epoch and uses that carrier's wallet/journal, physical grant pool and IMAP connections. Physical accounting therefore remains per real account. The owner Pump retains only logical byte/offset accounting and receives the exact Completion result;
- receiver-side DATA is authenticated/decrypted on the target carrier first, then routed by global stream ID into the owner Pump. Existing bounded `flow.Rx` offset reassembly handles out-of-order arrival; perf4 does not add a separate unbounded reorder queue;
- cleanup mappings retain both the physical target carrier and original logical owner. A physical cleanup proof on the target carrier releases the exact owner attempt IDs. If a target carrier disappears while it still holds striped physical debt for another owner, the affected owner is fail-stopped rather than receiving a fabricated refund; recovery retains the physical debt conservatively;
- a striped physical operation is cancelled when either its target carrier or logical owner dies. Cancellation before APPEND is `NotCommitted`; cancellation after APPEND start keeps the existing conservative `Unknown` semantics;
- server stripe-group metadata is bounded to eight simultaneously live session generations per user and fully detached generations are pruned before another group is admitted, preventing reconnect history from becoming an unbounded metadata table;
- trace output includes `stripe_select` with owner/target account IDs and a remote/local flag. This is intended to make real Rambler qualification prove that one TCP stream actually used multiple accounts instead of inferring striping from aggregate Mbps.

The perf4 loaded test uses four independent delayed TLS-IMAP carriers while opening both probe and bulk TCP streams through one owner lane. The existing strict loaded-latency limit remains 1.5 s median / 3 s max. It additionally requires payload throughput to exceed the former single-carrier region under the synthetic four-carrier path (10 Mbps aggregate bidirectional, 6 Mbps upload-only). These are build acceptance checks, not field-performance claims.

Perf4 intentionally bumps attachment to `ITH7` / wire version 7 with exact `DATA_BATCH | STRIPED_DATA | LOGICAL_CONTROL` capabilities. Although DATA frame encoding remains ITD3, a perf3 peer does not implement LogicalSession-wide stream routing; accepting it as compatible would permit authenticated DATA to arrive on a carrier that cannot route it to its owner. There is therefore no ITH6 fallback.

Remaining work after this slice is not another buffer increase: move OPEN/control ownership into a true carrier-independent `UserSession`, survive owner-carrier loss without killing live streams, add server-owned generation arbitration, then add measured carrier-quality/DRR-flowlet scheduling. S3 cumulative leases can later reduce control chatter, but durable-before-APPEND, Unknown debt, 8 MiB physical exposure per account and bounded receive state remain mandatory throughout.



### transport-s1-perf4-fix2

Build-only Android tooling correction over perf4-fix1. The transport, ITH7 wire contract,
striping scheduler, quotas, durability, and acceptance thresholds are unchanged. Android
compileSdk/targetSdk stay at API 37 so the previous Android lint modernization is retained.
Because Android 17 / API 37 is currently a preview SDK, the pinned sdkmanager install now
explicitly uses preview channel 3. Build Tools remain pinned at 36.0.0 as supported by AGP
9.1.1. Offline source checks require Gradle compileSdk/targetSdk to match the launcher's
pinned SDK to prevent version drift.

### transport-s1-perf4-fix3

Build-only Android provisioning correction. The successfully tested ITH7 / striped-DATA transport is unchanged.
The Android bootstrap now uses Command-line Tools 22.0 and installs the exact API 37.0 platform package
(`platforms;android-37.0`). Gradle keeps `targetSdk = 37` and explicitly sets `compileSdkMinor = 0`.
This fixes the obsolete-tools / wrong-package-name failure that reported `platforms;android-37` as unavailable.


### transport-s1-perf4-fix4

Build-only correction: AGP 9 disables Resource Values generation by default, while ITOM build types intentionally define `app_name` using `resValue`. The Android module now enables `buildFeatures.resValues = true`. Transport protocol, ITH7 striping, quotas, Noise, journal/wallet semantics, and acceptance thresholds are unchanged from perf4.


### transport-s1-perf4-fix6

Replay-fencing correction over perf4. ITH8 adds an authenticated client-probe/server-challenge exchange before Noise. A server lane snapshots the inbound UID tail, accepts only a fresh probe, and binds the client/server nonces into the Noise hello. Runtime receive starts after the accepted handshake UID instead of from UID zero. Historical ITH7/ITH8 mailbox contents therefore cannot reactivate a session after a service restart, and old DATA is not repeatedly fetched as live traffic. Physical DATA striping, quota limits, journal/wallet semantics and performance thresholds are unchanged.


### transport-s1-perf4-fix7

- Recovery matching is one-to-one: one physical `(folder, UIDVALIDITY, UID)` can prove and release at most one durable journal attempt even when multiple uncertain APPENDs have identical decoded digests. This prevents duplicate-UID `DeleteMany` batches and `IMAP_SCOPE` reconnect loops without manufacturing refunds.
- Server-side mail cleanup is serialized per user and paced across accounts. The original immediate "last carrier ended" purge was removed in fix10 because a transient all-carrier outage is not proof that the logical client session is finished. Automatic cleanup now runs only after the 15-minute idle TTL; the manager retains an explicit sequential full-pool purge.
- Cleanup is paced (250 ms between message batches, 1 s between accounts). A concurrent/new message makes a purge fail closed and retry later instead of being reported clean.
- Server manager `Почтовые аккаунты` adds a destructive full-pool option that purges all configured mailbox accounts sequentially. Provider-protected/system folders are emptied when deletion is refused; user folders are deleted when permitted. This administrative purge never erases ITOM physical-debt journals.

### transport-s1-perf4-fix9

- ITH9 replaces the initial probe-first handshake with a server-first boot challenge. Each waiting server lane appends one fresh HMAC-authenticated challenge after startup/recovery; the client fetches only the newest bounded mailbox tail and binds its fresh client nonce plus that server nonce into Noise.
- The client no longer depends on the server noticing an initial probe. A signed probe is sent only after two seconds as a fallback for an already-authenticated older lane, which can issue a reattach challenge.
- `mailbox.Recent` fetches at most the last 32 UID positions for pre-auth challenge discovery. This avoids both unbounded historical scans and periodic challenge spam while still letting a client started later discover the current VPS boot challenge.
- Pre-auth diagnostics now emit explicit `preauth_challenge_tx`, `preauth_challenge_rx[_recent]`, `preauth_probe_tx`, `preauth_hello_tx/rx`, and `preauth_reply_tx/rx` lifecycle events so handshake failures are localized without inferring state from generic APPEND/FETCH traffic.


## transport-s1-perf4-fix9 recovery/pre-auth correction

- ITH9 pre-auth probe/challenge/hello APPENDs are ephemeral authenticated handshake traffic and no longer consume the durable runtime control journal.
- A complete empty-folder inventory can release orphan LOCAL physical attempts left after provider/admin deletion; remote attempts are never released by this path.
- Stale pre-auth residue is deleted before empty-folder reconciliation so crash leftovers cannot keep a physically empty runtime journal wedged.
- This fixes `handshake_wait -> JOURNAL_LIMIT` when old control debt reached the 128-message physical admission cap.


### transport-s1-perf4-fix11

Carrier-loss resilience correction over ITH9/fix9; wire compatibility remains version 9.

- `mailbox.Recent(..., 32)` now splits the bounded pre-auth window into <=16-UID FETCH ranges. Reconnect no longer deterministically fails with `IMAP_SCOPE` once a mailbox contains more than one physical fetch page.
- DATA writer failures are isolated to the physical writer that failed. A poisoned client-side DATA socket is retired immediately, replacement dials are serialized and exponentially paced, and a recovered writer wakes the stripe scheduler. Server shared account writers already reconnect after an operation failure.
- The striped scheduler prefers a healthy physical carrier other than the logical stream owner, while retaining the owner as a last-resort DATA carrier. Carriers with no usable DATA writer are skipped.
- A physical APPEND with `CommitUnknown` no longer kills the logical owner Pump when durable journal/wallet bookkeeping completed successfully. The attempt remains in `VisibleWait`; authenticated progress/NACK and recovery decide whether the bytes need repair. Journal/fence/control failures remain fail-stop.
- C2S Usage/Release descriptors are published after a successful durable fence even when APPEND outcome is unknown, so a late-visible physical body can still be authenticated and absence can be proven without leaking the grant.
- UIDVALIDITY rotation is treated as loss of physical-location proof, not loss of the authenticated logical session. Live readers continue in the new mailbox incarnation; stale journal debt is retained conservatively until digest/absence recovery proves it.
- Immediate destructive cleanup on last-carrier detach is removed. Automatic mail destruction uses the existing serialized 15-minute idle TTL janitor; explicit manager full-pool purge remains available. This prevents a short provider outage from deleting folders while carriers are reconnecting.

### transport-s1-perf4-fix12

Gate/runtime correction over fix11; wire compatibility remains ITH9.

- Removed a duplicated `batchEventLink` test helper that made `datapath/pump_test.go` fail compilation before its regressions could run.
- Updated the reader-pacing regression to the fix11 policy actually shipped by the runtime: 40 ms hot-empty, 80 ms active-empty, then 250/500/1000/1500/2000 ms idle backoff while a moving UID tail remains delay-free.
- Fixed cumulative remote DATA descriptor binding. `UsageReport.Total.Messages` is the physical ordinal inside a multi-message grant; receive/GC now resolves that exact durable journal attempt. The old hard-coded ordinal 1 lookup failed after the first attempt had already been reclaimed and produced `REMOTE_DESCRIPTOR_MISSING` under sustained TLS-IMAP traffic.
- Added a regression proving later ordinals remain resolvable after ordinal 1 has been removed. No quota, batching, Noise, stream-routing, or wire-format change is introduced.


### transport-s1-perf5-gate4

Unreleased performance-gate candidate. ITH remains 9. DATA aggregation is serialized before parallel APPEND so workers no longer fragment one burst; bulk coalescing targets the largest safe eleven-record physical message and keeps short DATA on a 250 us fast path. Fast DATA receives the next available striped physical slot, bulk queueing covers the runtime flow window, and one saturated stream receives a 3 MiB BDP window while 1 MiB remains available for interactive traffic inside the unchanged 4 MiB logical/account/receive cap. GC/inventory uses a separate best-effort IMAP connection, DATA writers skip busy sibling sessions, and load diagnostics report physical batching/wait/cleanup metrics before enforcing strict >=10 Mbps directional throughput and RTT gates. This candidate is for `core` validation only until those gates pass.

### transport-s1-perf5-gate5

Unreleased core performance-gate candidate over gate4; ITH remains 9. Gate4 proved that the current batching/flow-control path sustains roughly 15 Mbps per direction in the synthetic delayed-IMAP test while steady idle/loaded RTT stays near 400-750 ms, but the first post-OPEN probe could still inherit the reader's pre-existing 250 ms..2 s zero-stream empty-mailbox sleep. Gate5 adds a coalesced local reader wake signal: latency-critical OPEN/OPEN_OK/initial WINDOW/grant activity interrupts only that synthetic sleep, never an in-flight IMAP command or UID ordering. Truly idle accounts keep the existing 250/500/1000/1500/2000 ms backoff. Gate5 also fixes the deferred-initial Pump regression test variable collision that prevented the datapath test package from compiling in gate4. Throughput/batching/windows/grant sizes and the >=10 Mbps directional acceptance threshold are otherwise unchanged.

### transport-s1-perf5-gate6

Unreleased core performance-gate candidate over gate5; runtime transport behavior is intentionally unchanged. Gate5 did not reach the load gate because two new regressions were themselves defective: `tunnel/io_test.go` omitted the `wire` import, and the deferred-initial Pump test treated independent periodic STATUS as if Pump had published the withheld initial WINDOW. Gate6 fixes only those tests. The deferred-initial regression now rejects an early CREDIT for the activated stream while permitting unrelated STATUS/control frames. The reader-wake cold-start change, perf5 batching/flow-control path, >=10 Mbps directional throughput threshold, and strict RTT thresholds are otherwise identical to gate5.



### transport-s1-perf5-gate7

Unreleased core performance-gate candidate over gate6; ITH remains 9. Gate6 restored cold idle RTT into the historical 400-800 ms range and retained 14-17 Mbps directional throughput, but a repeated full `all` run exposed rare loaded-tail spikes. Diagnostics tied the bidirectional outlier to a 557 ms wait for the reserved remote fast grant: the old fast reserve reused the ordinary 2 MiB/4-message cumulative lease, so tiny probe DATA fenced the lease after every fourth physical message and could wait for cleanup/reissue despite having consumed very few bytes. Gate7 separates QoS lease shapes without changing the wire format: one 512 KiB/32-message fast lease plus two unchanged 2 MiB/4-message bulk leases. Total remote byte reservation falls from 6 MiB to 4.5 MiB while bulk parallelism stays at two leases. Grant class is derived from the quota shape rather than arrival order, cumulative reuse now stops when either byte or message limit is exhausted, and regressions cover high ordinals and a fast probe burst crossing ordinal four. Throughput/RTT acceptance thresholds are unchanged.

### transport-s1-perf5-gate8

Unreleased core maintenance-correctness candidate over gate7; ITH remains 9 and all perf5 throughput/window/lease parameters are unchanged. Gate7's upload-only load case passed at 14.439 Mbps with 465/550 ms loaded RTT, but the bidirectional case terminated in GC with `maintenance: presence: IMAP_SCOPE`. Source analysis found a deterministic protocol race hidden by the generic scope code: during a UID presence FETCH, another cleanup connection can issue silent STORE + EXPUNGE. The IMAP server may then publish an unsolicited `FETCH (UID ... FLAGS ...)` before the tagged UID FETCH completion; go-imap routes a requested UID into the pending FetchCommand when no solicited row for that now-expunged UID was seen. The old shared `fetchSet()` required RFC822.SIZE on every routed row and therefore classified this valid concurrent update as `IMAP_SCOPE`. Gate8 gives `Present()` a bounded UID-only proof path: any requested UID observed in that command is conservatively retained for the current GC pass, while a later clean pass can prove absence. It never releases journal/quota debt on ambiguity. The strict general FETCH validator is unchanged semantically but now emits bounded `IMAP_SCOPE_FETCH_*` subreasons, and presence tracing records only structural UID-set facts. Regressions cover 40 requested UIDs with expunged holes, the concurrent STORE/EXPUNGE interleaving, and preservation of the high-level `IMAP_SCOPE` classification.

### transport-s1-perf5-gate9

Unreleased remediation/core-gate candidate over gate8; ITH remains 9 and the perf5 batching, 4 MiB account cap, 3 MiB stream window, fast/bulk lease shapes, and acceptance thresholds are unchanged. The post-gate8 audit found several independent long-tail/lifetime defects rather than one remaining throughput bottleneck. Physical carrier selection no longer reserves an APPEND slot while a batch may spend seconds waiting for a storage grant; the scarce slot is acquired only immediately before durable spend/start and APPEND, with separate grant-wait and physical-slot-wait diagnostics and live writer-count admission. A 0->1 logical stream transition wakes every carrier reader so no stripe member can remain in a pre-existing 250/500/1000/1500/2000 ms idle backoff. Successful OPEN state is removed promptly, canceled OPENs stop retransmission, and an individual OPEN deadline is stream-local instead of terminating the whole lane. The existing ITH9 lifetime bound of 4,096 odd stream IDs is preserved for compatibility, and the client supervisor now rotates to a fresh authenticated logical-session generation on exhaustion rather than permanently locking out new TCP streams. Automatic idle janitor cleanup is conservative and deletes only independently recognizable pre-auth garbage; authenticated DATA/control mail is never purged outside durable journal proof. Semantic UIDVALIDITY change no longer poisons an otherwise usable mailbox connection. If an optional isolated reader is refused by the provider connection limit, activation now continues through the serialized base-DATA fallback instead of publishing a carrier with no usable DATA writer. The load harness continuously watches carrier-lane exits and reports per-probe RTT plus separate grant/stripe wait maxima so a future tail cannot be misreported while a carrier has already failed. New regressions cover group-wide reader wake, late-carrier wake, selection-vs-slot separation, dynamic physical capacity, bounded stream-space exhaustion/rotation, and janitor retention of authenticated mail. Stale lower carrier epochs are rejected instead of replacing a newer attachment, and authenticated cleanup now treats only an already-retired `WALLET_MISSING` grant as idempotent while preserving `WALLET_STATE` as a fail-closed accounting divergence.



### transport-s1-perf5-gate10

Audit-remediation/performance-stability candidate, still ITH9 and not a release. This gate keeps the gate9 lifecycle/accounting fixes and adds the following transport corrections without loosening the performance thresholds or changing the wire format:

- interactive DATA has a reserved fast batch worker so short records cannot wait behind all bulk writer goroutines; fast and bulk records remain physically separated;
- bulk/fast queue wait is measured separately from grant and striped physical-slot wait;
- cleanup is bounded per maintenance pass and owner-carrier cleanup is deferred while an application stream is active unless accounting pressure requires reclamation;
- cleanup/stripe coordination prevents latency-critical physical DATA from competing with cleanup I/O on the selected carrier;
- general UID FETCH remains fail-closed, but transient unsolicited UID-only/UID+FLAGS rows that go-imap can attach to a pending exact UID FETCH are drained and retried up to three bounded attempts before `IMAP_SCOPE_FETCH_PARTIAL_RETRY_EXHAUSTED`;
- the load gate continuously checks runtime lane failures and preserves the existing >=10.0 Mbit/s directional, idle and loaded RTT thresholds.

Local Go 1.27.1 qualification before packaging: five consecutive bidirectional load tests and five consecutive upload-only load tests passed; the exact `go test -mod=readonly -json -count=1 -timeout=120s ./...` suite and `go vet -mod=readonly ./...` also passed. The executable source ZIP must still pass its own `core` launcher gate before any `all`/deployment step.

### transport-s1-perf5-gate11

Field-reliability and Android energy-efficiency candidate over gate10; ITH remains 9 and all cryptographic, durable-accounting, BDP/window, fast/bulk lease, and performance-threshold contracts remain unchanged. Gate10 passed synthetic load qualification but a real Android/VPS session entered a false-live state shortly after connection: one carrier could lose forward progress while still appearing `ready`, new TCP OPEN ownership could remain pinned to that half-open lane, a DATA batch could wait on that selected carrier's empty grant queue despite grants on sibling carriers, and detaching a physical carrier could recursively cancel logical owners whose DATA attempts had been striped through it. Maintenance timeout/control bursts could amplify the failure.

Gate11 contains that failure domain. Cross-carrier physical-attempt ownership is retained in LogicalSession scope so a physical detach does not cancel unrelated TCP owners and durable cleanup/recovery can still attribute the attempts fail-closed. Client DATA selection is grant-aware and only reserves a carrier that is healthy and already has the required grant class. New OPENs require recent authenticated progress; an OPEN that receives no result within four seconds is abandoned safely, its owner is temporarily quarantined, and `Client.Open` retries another healthy carrier while a late OPEN_OK is consumed and closed by the old actor. Transient cleaner/maintenance transport failures no longer terminate the application lane; cleaner redial is asynchronous, cleanup is retried, and accounting/state divergences remain fatal. Cleanup quota/status control uses a separate bounded queue so maintenance bursts cannot fill the interactive urgent queue.

Fast DATA coalescing also now drains records that are already queued before honoring the 250 µs coalescing deadline; the timer bounds waiting for future records and can no longer win a scheduler-jitter race that fragments a ready four-record burst.

Android settings schema 5 adds `diagnostics_enabled`, default false. When disabled, the native client does not create packet trace files, per-line diagnostic objects, event history, or the extended observed-IMAP wrapper; the VPN service also avoids calling `diagnostics()` and lowers routine stats/notification polling to once every two seconds. The diagnostics screen can enable it explicitly and restarts an active VPN so instrumentation state changes immediately. RTT presentation is green below 400 ms, yellow from 400 through 799 ms, and red at 800 ms or above. The foreground notification uses the same presentation string and always includes download speed, upload speed, and RTT.


### transport-s1-perf5-gate17

Latency/diagnostic-efficiency refinement over gate16; ITH remains 9. A non-reference field run on a heavily loaded corporate network stayed alive, but idle RTT was around one second and several late OPEN_OK results were observed tens of seconds after their original OPEN owner had already been abandoned. The same diagnostic export rotated roughly 48 MiB of trace within only a few seconds, proving that full encrypted-packet base64 plus per-frame chatter could itself become a meaningful device I/O/CPU workload while tracing.

Gate17 keeps packet SHA-256/UID/session/counter metadata but no longer writes every encrypted MIME body into JSON. DATA and routine WINDOW/RECEIPT/MAIL_RECEIPT/GC_STATUS frame events are deterministically sampled; latency-critical control and any routine control waiting at least 100 ms remain visible. New TCP OPEN ownership is no longer blind round-robin: among healthy carriers it prefers recent, lower-RTT and less-queued lanes, sharing only among near-equivalent candidates. Periodic Echo/PING phases are deterministically staggered per account/side instead of bursting across all carriers together.

The receive mailbox still mixes DATA and control. To reduce head-of-line latency without changing folders or ITH9, an OPEN marks the dedicated reader hot for the existing four-second owner-result window. During that interval PageTail limits each UID FETCH to four messages instead of sixteen, so an OPEN_OK near the front of a burst is returned to the lane actor without waiting for up to twelve later large DATA bodies. Outside interactive OPEN windows the normal 16-UID drain remains unchanged for throughput.

The same field trace exposed a separate upload-side stall: bulk `stripe_select_wait` reached roughly 3-9 seconds and the waits ended at the arrival time of replacement 2 MiB storage grants. Gate17 therefore keeps the normal two bulk leases but, once the peer fences one exhausted bulk lease, may pipeline one temporary replacement while physical cleanup/status for the closed lease is still in flight. This is not a permanent third reservation and does not enlarge any grant or message limit. The existing 8 MiB journal bound and 512 KiB local fast reserve remain authoritative, so the extra replacement is skipped under full-duplex pressure when the byte headroom is unavailable. Diagnostics now report grant-selection wait and the count of these temporary bulk replacements.

### transport-s1-perf5-gate16

Event-driven receive candidate over gate15; ITH remains 9 and the gate15 receipt-window, cleanup-fairness, authenticated reattach, grant/accounting and 25-second transport-health contracts are unchanged. Field diagnostics from the gate15 speedtest showed that the isolated reader socket itself could spend 4-13 seconds inside successful UID FETCH operations while each Android carrier accumulated tens of thousands of polls, most of them empty. This made the public IMAP backend, rather than local queue admission, the dominant receive-latency source.

When the authenticated provider advertises RFC 2177 IDLE (or IMAP4rev2), the dedicated receive connection now enters IDLE after its bounded UID tail is fully drained. An unsolicited mailbox-size/EXISTS update wakes the reader immediately; the reader then drains bounded UID ranges with no artificial delay until empty and returns to IDLE. A two-second audit ends IDLE and performs the existing SELECT/UIDVALIDITY verification, preserving the previous mailbox-recreation safety bound if a provider misses a notification. A mailbox update racing with the last empty FETCH is treated as a wake rather than discarded. Providers that do not advertise IDLE keep the exact gate13 2 ms hot / 5 ms active polling fallback. Control, DATA writers and cleanup remain on their independent connections, so IDLE never serializes application writes.

Diagnostics now report whether the reader is using IDLE plus aggregate IDLE wait/notification/audit counters. No IDLE command is traced per-cycle. New regressions cover notification wake, audit timeout reuse of the same authenticated connection, the empty-FETCH/IDLE race, repeated IDLE cycles, and the tunnel reader not continuing an empty polling loop while IDLE is active.

### transport-s1-perf5-gate15

IMAP pressure and diagnostic-overhead reduction over the first field-working gate13
prototype; ITH remains 9 and transport/accounting semantics are unchanged.

Field diagnostics showed that an active 10-carrier Android session could issue roughly
730 reader `fetch_page` operations per second on the client alone, with the server
adding roughly 300 per second. The old active-reader policy used a fixed 2 ms hot / 5
ms active delay after every empty UID-tail poll, and stripe-wide activity kept every
carrier at that rate. This created more than one thousand polling IMAP commands per
second across both endpoints even with only a handful of TCP streams.

Gate14 keeps zero-delay draining whenever mailbox UIDs are actually advancing, but
empty hot and active polls now use short adaptive backoff ladders (hot 5/10/20/30/40
ms, active 10/20/40/60/80 ms). Any mailbox progress resets the ladder immediately and
latency-critical local control still wakes a sleeping reader, so sustained DATA drain
and OPEN/WINDOW responsiveness do not inherit the idle delay.

Diagnostics no longer writes an `imap_begin`/`imap_end` pair for every successful fast
empty live `fetch_page`. Non-empty polls, failures, and polls taking at least 100 ms
remain fully traced, while `FetchPolls` and `EmptyFetchPolls` aggregate counters are
reported in line diagnostics/health events. This preserves useful provider-stall and
packet evidence without allowing normal empty polling to dominate the trace itself.

### transport-s1-perf5-gate13

Field speedtest reliability correction over gate12; ITH remains 9 and the existing
transport watchdog, journal caps, accounting rules, DATA grant shapes and
qualification thresholds remain unchanged.

Gate12 fixed the authenticated-progress clock, but sustained field traffic exposed
a separate cleanup-scheduling starvation. Continuous remote DATA reclamation could
keep `remoteGC` non-empty and repeatedly win the single cleanup slot. Local control
journal reclamation then failed to run often enough: affected lanes reached the
existing `writeControls` hard stop at 112 held control records while IMAP FETCH
remained healthy and the control queue grew into the hundreds. Once control APPENDs
stopped, PING/PONG/WINDOW/grant progress disappeared and the unchanged 25-second
transport watchdog correctly rotated the lane.

Gate13 gives a due, pressured local cleanup class one bounded turn before remote
cleanup. Local control is checked first, followed by local DATA and presence. The
existing pressure thresholds and bounded cleanup workers are retained, so remote
reclamation resumes after the local turn and maintenance cannot monopolize the lane.
A regression covers simultaneous remote-GC pressure and a 48-message local control
journal and verifies that local control pressure wins. The 80/112 control admission
limits are not enlarged and no timeout is relaxed.

### transport-s1-perf5-gate12

Field reliability fix after gate11 Android/VPS diagnostics showed deterministic
`TRANSPORT_TIMEOUT` reconnect waves on otherwise healthy IMAP carriers.

- `acceptRecord` now calls `markRemoteProgress()` after a successfully
  authenticated, replay-checked and processed peer record instead of updating
  only `lastRX`; PING/PONG, OPEN/OPEN_OK, WINDOW/STATUS, receipts and client-side
  DATA therefore refresh the transport-health clock.
- The 25 s transport watchdog remains unchanged and still detects genuine loss
  of authenticated peer progress; ITH9 and all accounting semantics are unchanged.
- A regression forces `lastProgress` stale on a live in-memory lane and verifies
  that an authenticated PONG restores both transport health and OPEN eligibility.



### transport-s1-perf5-gate18

Latency correction over gate17; ITH remains 9. Field diagnostics showed that IMAP IDLE solved the previous empty-FETCH command storm but could add roughly a provider notification interval to interactive RTT: a local PING/OPEN could be appended promptly and the peer response could already exist in the mailbox while the dedicated reader remained inside IDLE waiting for a delayed unsolicited EXISTS. Gate18 adds hybrid IDLE without returning to permanent polling. A latency-critical local control frame arms the existing bounded hot-response window and signals the reader. If the reader is currently in IDLE, the mailbox adapter ends only that IDLE command with `DONE`; it does not close or reconnect the authenticated reader socket. The reader immediately checks the UID tail and, while the hot window is active, uses the proven 2 ms empty-poll cadence before returning to event-driven IDLE. A queued kick in the empty-FETCH-to-IDLE race is consumed as an immediate recheck rather than lost. Diagnostics expose `idle_kicks` separately from provider notifications and periodic IDLE audits. Regressions verify in-flight IDLE interruption, queued-kick handling, socket usability after `DONE`, and that the lane stays out of IDLE during the hot response window.

The Go load qualification criteria are now directional throughput >=30.0 Mbps and strict loaded RTT max <700 ms. Idle/cold RTT remains reported for diagnosis but is not an independent release threshold. Repeated qualification exposed a throughput-only tail caused by bulk leases fencing on the old four-message ordinal cap while using far less than their 2 MiB byte allowance. Gate18 therefore keeps each bulk lease at exactly 2 MiB and keeps two normal bulk leases, but raises its bounded ordinal budget to 32 messages. The bulk scheduler also fences a lease once less than one maximum-size 512 KiB physical APPEND of byte headroom remains, so a reused grant cannot later fail with `WALLET_LIMIT` merely because the next encoded batch is larger than the residual bytes. The normal remote byte reservation stays 4.5 MiB including the 512 KiB fast lease, the temporary replacement remains bounded by the existing 8 MiB journal exposure, and the 512 KiB per-message cap is unchanged. No wire, accounting, cleanup, watchdog or Android routing semantics change in this gate.

### transport-s1-perf5-gate19

Measurement-only continuation over gate18; ITH remains 9 and no transport/accounting limit is changed. The existing `TestLoadedApplicationRTT` and `TestLoadedApplicationUploadWithTrace` retain the same directional >=30.0 Mbps and loaded RTT max <700 ms release-regression thresholds. Gate19 deliberately does not tune polling, windows, grants, worker counts, cleanup cadence, MIME caps or watchdogs before the full-duplex bottleneck is measured.

A new opt-in `TestLoadedApplicationProductionHarness` exercises the production VPS DATA path through the shared per-account `accountRuntime` instead of constructing every server lane with independent DATA writers. It accepts 1–10 accounts, 1–2 users and 1–8 application streams per direction, runs upload and download as independent saturated application flows, performs streaming deterministic content verification, uses a common warmup/measurement interval rather than a 32 MiB stop condition, and sends authenticated application DATA echo probes for the whole measured load interval. Throughput is computed from bytes actually delivered and verified at the receiving application endpoint. The harness refuses to claim overlap unless at least one RTT sample is observed while both upload and download have already made delivery progress. It is skipped unless `ITOM_LONG_LOAD=1`; optional `ITOM_LOAD_MIN_MBPS` and `ITOM_LOAD_MAX_RTT_MS` turn a chosen profile into an explicit qualification assertion.

Diagnostics are extended without plaintext payload logging. `observedMail` now separates successful DATA and control APPEND counts/MIME bytes; DATA writers record logical payload bytes, physical batch count, records per batch and pre-MIME batch body bytes. The shared `accountRuntime` exposes bounded snapshots for live/busy workers, queued requests, pending MIME bytes, completed/failed work, queue/service maxima and per-user completed MIME bytes. The existing heavy tests also print these new DATA/control and batching counters, so repeated gate18-style 5x runs can identify partial batches or MIME/control amplification before changing runtime behavior.

The documentation mismatch in gate18 is corrected here: the current cumulative DATA lease message/ordinal budget implemented and asserted by code is 32, not the stale 8/16 values in README/history prose. This is distinct from `wire.MaxDataBatchRecords`, which remains 16 encrypted DATA records per one physical ITD3 batch.



### transport-s1-perf5-field2

Grouped block 1: compatible journal profiles and explicit audited offline migration; strict application payload/EOF verification; fixed measurement clocks; real supervisor fixtures and cancellable provider-test pacing. ITH9/capabilities and governor-off state are preserved. Product owner and repeated-account recovery remain unqualified. See PROJECT_HANDOFF.md and DEVELOPMENT_PLAN_GROUPED.md.


## field3 — logical owner and account recovery

The ITH9 framing stays version 9, but the exact authenticated capability set now
includes LOGICAL_CONTROL. Field2 peers fail capability negotiation. An APK and
VPS binary must come from one successful field3 `all` build.

One logical actor, Pump and TCPExit budget per configured account survives its
physical carrier. OPEN_OK/WINDOW/RECEIPT/NACK/FIN/RESET and DATA route within the
same authenticated user/logical session/generation; quota, Usage and cleanup
remain on the original physical account. Replacement carriers borrow the actor
without creating another 4 MiB/64 socket/4096 metadata budget or TCP target dial.
The existing 256-frame incoming queue budget is divided into 32 actor frames
and 224 Pump inputs; IDs and tombstones remain globally bounded to 4096.

Definitive pre-APPEND cancellation may retry only after successful cancellation
of every Prepared reservation. NotCommitted re-enters the byte scheduler;
Unknown stays charged and uses authenticated repair. Physical writers join
before the old journal locks are released. Self-owned outgoing attempts are
also retained as logical links for the replacement carrier's recovery proof.
The 25-second authenticated progress watchdog, logical limits and physical
profiles are unchanged. Local readiness never refreshes progress.

Correlated actor snapshots distinguish READY, usable/issued fast/bulk grants,
physical DATA/control debt and cleanup. Admission/cleanup experiments that
failed sustained acceptance are in FIELD3_SERVICE_CANDIDATE.patch.disabled;
they are absent from the runtime. See PROJECT_HANDOFF.md for qualification.


Допуск replacement carrier к DATA и stream control требует matching reply на
новый authenticated Echo его текущего crypto epoch. READY/старый RTT сами по
себе не подтверждают rejoin. Echo стоит в normal FIFO после CoordinatorAck,
поэтому не обгоняет bootstrap. Потоковая маршрутизация не обходит Gate/Bootstrap
activation checks. До fresh echo используются уже готовые sibling carriers;
никаких дополнительных физический квот или продления watchdog нет.


После CommitUnknown отправитель может приблизить один status-probe в пределах
обычного ProbeInterval (2 секунды), чтобы не добавлять полный periodic interval
к восстановлению. Остальные Unknown completions того же APPEND не создают
неограниченный burst. Unknown не становится NotCommitted, долг сохраняется;
DATA repair по-прежнему требует authenticated NACK. Watch deadline и transport
watchdog не продлеваются. Регрессия проверяет ранний probe, отсутствие refund/
blind retry, авторизацию через настоящий receiver response и bounded burst.


## FIELD67 BINARY APPEND (RFC 3516)

Per authenticated IMAP socket, the adapter automatically enables a literal8
`APPEND ~{N}` for a canonical DATA message **only when** the provider
advertises `BINARY` (or IMAP4rev2). This is evaluated independently on each
mail account: no hostname assumption is made, even for Rambler. A provider
without BINARY continues with standard base64 MIME. For providers that advertise
BINARY but break binary payloads, set `mail.disable_binary_append: true` on
that account to force the MIME path. A definite tagged NO fences that socket and suppresses BINARY on fresh
connections for the affected account for 30 minutes; a failed/ambiguous APPEND is NEVER
resent without journal reconciliation.

The physical message uses `Content-Transfer-Encoding: binary`; an encrypted
ITD3 body goes directly in its MIME part and is carried as RFC literal8. All
transport/accounting layers still see *canonical base64 MIME* after BODY or
BINARY FETCH. Its byte size, message digest, append hint, Unknown APPEND proof,
UID identity, and durable debts are unchanged, so upgrading existing journals
does not require migration. The physical size saved by BINARY is measured
separately and is not mistaken for a quota refund.

BINARY support depends on actual provider capability and correctness; tests
with a synthetic BINARY-capable IMAP server do not prove a particular public
provider's behavior, nor do they qualify 40/40 Mbps on a phone.
