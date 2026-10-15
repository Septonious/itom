# ITOM project handoff — `transport-s1-perf5-field1`

Date: 2026-10-02  
Product version: `0.9.0-dev1`  
Protocol: ITH9  
Current candidate: `transport-s1-perf5-field1`  
Status: **field-test candidate, not a qualified release**

## 1. Executive summary

ITOM is an experimental TCP-over-IMAP transport with an Android VPN client and a Linux/VPS endpoint. Application TCP streams are encrypted/framed, striped across a pool of ordinary IMAP accounts, transferred as mail through APPEND/FETCH, reconstructed at the peer, and forwarded to the real TCP destination. The project deliberately preserves generic IMAP fallback, durable accounting before APPEND, replay/nonce safety, bounded queues, proof-based physical cleanup, and isolation of failures between carriers/streams.

The immediate historical goal was to recover a stable >=30/30 Mbit/s full-duplex regression gate while keeping loaded RTT <700 ms in the local four-account 80-ms-command-delay model. That is now mostly achieved: the final field1 qualification produced 4/5 bidirectional passes, with throughput 38.93–44.39 Mbit/s per direction. The one failure was not throughput but a single 715 ms RTT sample. Upload-only passed 5/5 at 38.97–42.67 Mbit/s with RTT max 470–529 ms.

A production-path harness also exists. It uses shared VPS `accountRuntime`, independent upload/download streams, real TCP endpoints, TLS/TCP IMAP connections, APPEND/FETCH, streaming content verification, and optional provider/fault models. It is not a pure arithmetic simulation. Provider behavior is synthetic, but the measured payload still traverses the actual ITOM tunnel and IMAP adapter.

The experimental latency-first governor requested later in development is **intentionally disabled in this candidate**. Its latest source/tests are preserved under `docs/experimental/*.disabled` for future work. Do not re-enable it while doing initial field testing or while fixing the higher-priority progress/recovery problems listed below.

## 2. User goals and non-negotiable priorities

Long-term product target:

- one active user using the common pool;
- 100 Mbit/s useful application download and 100 Mbit/s useful upload simultaneously, if the provider pool can physically sustain it;
- application RTT under load must matter more than headline bandwidth;
- future latency governor target: initial authenticated IMAP RTT + 400 ms;
- future governor must never throttle a path below 10 Mbit/s useful throughput, and must avoid mode chatter when physical capacity fluctuates around 9–11 Mbit/s;
- Android client + Linux/VPS must be built from the same exact revision;
- common pool target is 10 IMAP accounts and two users, but 100/100 **per each of two users** would mean 200/200 aggregate and is a separate capacity claim;
- generic IMAP support must remain; provider-specific optimizations must be capability-gated.

Correctness is more important than throughput. Never obtain speed by weakening the following invariants:

1. durable charge/state transition before starting APPEND;
2. `Started -> Unknown` uncertainty on ambiguous failures;
3. no double release, double delivery, replay acceptance, or nonce reuse;
4. exact cumulative descriptor/ordinal semantics;
5. physical deletion requires proof tied to ownership, digest/location and UIDVALIDITY;
6. RECEIPT/TTL alone does not prove physical deletion;
7. bounded RAM, queues, metadata, grants and physical exposure;
8. fast DATA/control resources must stay protected from bulk traffic;
9. cleanup must remain bounded and fair;
10. recent authenticated progress, not socket existence alone, determines carrier health;
11. the 25 s transport watchdog must not be increased merely to hide stalls;
12. one broken carrier/account must not recursively kill unrelated stream owners;
13. Android and VPS protocol changes must be versioned and deployed together.

## 3. Current codebase and architecture

Read these first:

- `README.md` — build/use entry point and revision notes;
- `docs/TRANSPORT_CONCEPT.md` — intended full architecture and invariants;
- `docs/TRANSPORT_S1.md` — implemented transport lineage;
- `core/tunnel/` — IMAP transport, lanes, striping, shared account runtime, cleanup and load tests;
- `core/datapath/` + `core/flow/` — TCP records, batching, windows/receipts and delivery;
- `core/mailbox/` — generic IMAP adapter and deletion semantics;
- `core/journal/`, `core/wallet/` — durable state/accounting;
- `native/` — native bridge/server pieces;
- `android/` — Android UI/VPN path;
- `tools/build.py` — authoritative source/build/check launcher.

Important runtime concepts:

- A `LogicalSession` groups physical IMAP carriers.
- Application TCP streams are not supposed to be tied to the life of one physical carrier.
- DATA is striped across eligible carriers, with grant-aware selection.
- The VPS production path shares two DATA writers per account through `accountRuntime`.
- Control/reader/cleaner ownership is still more lane/user specific; account-wide coordination is incomplete.
- Dedicated readers use IMAP IDLE with bounded `DONE`/hot-read behavior for local latency-sensitive work.
- Physical mail debt remains charged until proof cleanup.

## 4. What changed after gate19 and is retained in field1

The field1 candidate is based on the stable pre-governor transport line, not gate51–53 runtime experiments. Important retained changes relative to gate19 include:

### 4.1 Bulk collector backlog-before-timer

`core/datapath/pump.go` consumes already queued bulk records before allowing an expired coalescing timer to end a batch. The deadline limits waiting for *future* records; it should not randomly beat data that is already available. This improved batch fill and removed one source of run-to-run throughput loss.

### 4.2 Cleanup starvation fix when every ready carrier is an owner

`core/tunnel/cleanup.go` and `stripe.go` no longer defer remote cleanup everywhere merely because every ready lane owns a stream. If all ready carriers are owners, at least bounded remote cleanup is allowed so finite grants cannot be exhausted indefinitely. Owner control-path preference remains otherwise intact.

### 4.3 Faster local cleanup pressure response

The local cleanup interval was reduced from 1500 ms to 1000 ms. This was introduced after diagnostics showed long grant/data waits correlated with physical-debt/headroom pressure.

### 4.4 Grant replacement/lifecycle correction

When `StorageRelease` fences a grant, it is no longer counted as a usable grant while physical proof deletion is still pending. Later physical release can immediately retry restoration of the normal baseline grant window. This addresses the case where a replacement attempt failed under temporary headroom pressure and was never retried promptly.

### 4.5 Current physical exposure limits

The stable candidate currently uses a DATA physical/journal limit of **12 MiB / 192 messages** (and corresponding pending DATA MIME hard cap), increased from the earlier 8 MiB / 128-message configuration. This change helped throughput but is correctness/performance-sensitive. Treat it as part of the current candidate that needs continued profiling; do not raise it further without measuring RAM, debt, cleanup rate and provider behavior.

### 4.6 Observed UIDPLUS cleanup fast path

`mailbox.DeleteObservedMany` allows exact UID-set deletion without a second full-body FETCH when the messages were already fetched, decoded, digest-verified and durably bound, and the server supports UIDPLUS/IMAP4rev2. UIDVALIDITY is rechecked before deletion. Providers without the capability use the conservative existing `DeleteMany` path. This optimization is correctness-sensitive; keep its tests and audit its proof assumptions before changing it.

### 4.7 Diagnostics

Diagnostics distinguish local vs remote grant waits, headroom waits, held DATA bytes/messages, cleanup classes, WINDOW/RECEIPT counts and maxima, DATA/control MIME, batch fill and shared account runtime queue/service counters. Keep diagnostics compact and disabled by default on Android field runs unless explicitly needed.

### 4.8 `accountRuntime` completion ordering race

The worker releases its busy/pending accounting before signalling request completion. This avoids a completed request being observed while the runtime snapshot still reports its bytes/worker as pending/busy. The change is about observability/accounting ordering and should not intentionally alter wire behavior.

### 4.9 Production harness improvements

The opt-in production harness now:

- opens all application TCP streams before releasing bulk generators (start barrier);
- runs independent upload and download, not echo-coupled throughput;
- uses streaming deterministic content verification;
- records common time windows and application RTT probes;
- can model provider byte-rate caps, time-varying caps, reproducible random line drops and account outages;
- still transfers the measured payload through real ITOM tunnel logic and TLS/TCP IMAP APPEND/FETCH.

## 5. Latency governor work — PAUSED

The latest latency governor prototype is preserved as:

- `docs/experimental/latency_control_gate51.go.disabled`
- `docs/experimental/latency_control_gate51_test.go.disabled`

Do **not** wire these files back into runtime as part of the first Codex task.

Requirements for a future redesign:

- baseline = authenticated IMAP server RTT measured around first usable connection, excluding DNS/TCP/TLS setup;
- target loaded application RTT = baseline + 400 ms;
- latency has priority over throughput above the minimum useful rate;
- governor must not be allowed to throttle useful throughput below 10 Mbit/s;
- physical links oscillating around 9–11 Mbit/s need filtered hysteresis, not a threshold toggle;
- fast DATA/control/WINDOW/RECEIPT/cleanup must not be delayed behind bulk shaping;
- avoid sleep-based throttling; preferably regulate bounded bulk pressure/concurrency/credit in a way that cannot deadlock progress;
- the previous prototype could become asymmetric between directions, cut upload to ~20 Mbit/s while download stayed ~70 Mbit/s, and contributed to long-run stalls. Revisit only after transport progress/recovery is robust.

## 6. Verification completed for field1 source behavior

Toolchain used locally:

- Go `1.27.1` from the supplied offline toolchain archive;
- supplied offline module cache;
- `GOPROXY=off`, `GOSUMDB=off`.

Completed checks on the stable tree before revision-only metadata update:

- `gofmt`: PASS;
- non-heavy `core/tunnel` suite: **81 tests PASS**;
- all Go packages before `tunnel` in `go test ./...`: PASS;
- `go vet ./...`: PASS;
- heavy tests below were run through a compiled exact stable-tree tunnel test binary.

### 6.1 Bidirectional legacy heavy gate — 4/5 strict passes

| Run | Directional useful Mbit/s | Loaded p50 ms | Loaded max ms | Result |
|---|---:|---:|---:|---|
| 1 | 38.930 | 371 | 644 | PASS |
| 2 | 41.248 | 419 | 586 | PASS |
| 3 | 44.389 | 445 | **715** | **FAIL: max RTT >700 ms** |
| 4 | 39.011 | 435 | 552 | PASS |
| 5 | 41.011 | 403 | 616 | PASS |

Throughput passed 30 Mbit/s in every run. The failure is a real latency-tail warning; do not relabel this as 5/5.

### 6.2 Upload-only heavy gate — 5/5 passes

| Run | Useful Mbit/s | Loaded p50 ms | Loaded max ms | Result |
|---|---:|---:|---:|---|
| 1 | 40.206 | 358 | 529 | PASS |
| 2 | 41.392 | 407 | 487 | PASS |
| 3 | 38.969 | 400 | 476 | PASS |
| 4 | 42.666 | 426 | 494 | PASS |
| 5 | 42.626 | 418 | 470 | PASS |

### 6.3 Production-path smoke, 4 accounts / 1 user / 4 streams each direction

Configuration: 80 ms synthetic IMAP command delay, 3 s warmup, 5 s measured interval, diagnostics on, no provider cap/faults.

Observed:

- upload: **25.008 Mbit/s**;
- download: **67.684 Mbit/s**;
- RTT p50: **552 ms**;
- RTT max: **704 ms**;
- all 5 probes had bidirectional overlap;
- all four `accountRuntime` instances had `failed=0`;
- the test completed and content verification passed.

The asymmetry and large grant/pump waits show that this short smoke is not a product qualification.

### 6.4 10-account production smoke

A 10-account / 1-user / 4-stream run began strongly: first window approximately **89.5 Mbit/s upload / 105.0 Mbit/s download**, RTT ~881 ms. The test did not complete inside the external execution limit and therefore is **not a successful result**. The incomplete run reinforces the long-run progress problem below.

### 6.5 Historical long-run production result

Earlier pre-governor production testing demonstrated that >100/100 burst capacity exists, but a ~30 s common interval fell to about **65.45 Mbit/s upload / 78.89 Mbit/s download** with RTT max about **1724 ms**. This is why short bursts must not be treated as 100/100 qualification.

## 7. Highest-priority unresolved problems

### P0 — Multi-account long-run progress / stalls

This is the first problem Codex should analyze. With more accounts and sustained full-duplex load, the production harness can stop making timely progress or fail to finish even after a strong initial window. Determine whether the dominant wait is:

- grant lifecycle/headroom;
- physical DATA exposure/debt cleanup;
- pump/collector queueing;
- accountRuntime admission;
- control descriptor delivery;
- reader visibility/FETCH;
- reorder/flow window;
- teardown-only harness deadlock versus live datapath stall.

Do not assume the harness is wrong or the transport is wrong before tracing the exact wait chain. Add bounded progress watchdog diagnostics if necessary, but do not increase transport watchdog time to mask it.

### P0 — Carrier/account outage isolation and recovery

Fault experiments showed that a full IMAP account outage can still lead to application EOF, slow recovery or teardown hangs instead of cleanly degrading onto sibling accounts and later restoring capacity. The desired behavior is:

- existing application TCP streams survive loss of one account if useful sibling capacity remains;
- grants/owners associated with the lost carrier become bounded `Unknown`/recovery work, not global cancellation;
- new connections to the down account back off rather than storm;
- healthy accounts continue transferring;
- when the account returns it re-enters the pool safely;
- no namespace/cipher/grant mixing between users.

The provider/fault harness exists to reproduce this with fixed seeds.

### P1 — Grant/headroom/cleanup pressure under sustained full duplex

Long production runs show seconds-long local/remote grant waits, grant-selection waits, pump queue waits and high headroom wait counts. Physical-debt cleanup and logical credit must be separated conceptually. Investigate the lifecycle from APPEND accepted -> received -> descriptor usable -> application delivered -> receipt/window -> physical proof deletion.

Do not solve this merely by raising exposure limits again. First measure:

- oldest physical debt age;
- usable grant count/headroom per lane;
- time from StorageRelease fence to replacement usable grant;
- cleanup batch service time and bytes/messages reclaimed;
- actual writer occupancy while bulk demand exists;
- application flow-window turnaround.

### P1 — RTT tail stability

The legacy gate had one 715 ms sample in five final bidirectional runs. Average/median latency is acceptable, but tail spikes are still present. Before restoring an active governor, identify whether spikes correlate with cleanup, grant starvation, control wait, reader wake or provider scheduling.

### P1 — Capacity adaptation under provider throttling

Provider cap tests (100/50/25/10 Mbit/s and abrupt transitions) showed poor utilization and severe RTT/progress behavior in some experimental branches. The transport should degrade gracefully when available provider capacity changes instead of accumulating debt/queues at the old rate.

### P2 — Shared account runtime / two-user fairness

Current production VPS shares DATA writers per account, but full account-wide connection/command budgeting is incomplete. Round-robin by message is not byte/time fairness. Test A-only, B-only, A-bulk+B-interactive and both-bulk. Keep per-user cryptographic/namespace ownership isolated.

### P2 — Real provider capacity qualification

Ten Rambler accounts reaching sustained 100/100 is still a **development target**, not a demonstrated provider capability. Measure APPEND/FETCH/cleanup full lifecycle with incompressible encrypted-like payload and increasing pool sizes 1 -> 4 -> 10. Distinguish provider/network ceiling from code ceiling.

### P2 — Android/VPN datapath overhead

After core progress is stable, compare transport core, native/SOCKS and Android TUN/gVisor separately. Do not start by arbitrarily changing MTU/TCP stack.

### P3 — Reintroduce latency-first governor

Only after P0/P1 progress bugs are fixed. Preserve the requirements in section 5. A good result is explicitly allowed to be, for example, 40 Mbit/s at ~600 ms rather than 100 Mbit/s at 1700 ms.

### P3 — Sustainable 50/50 -> 80/80 -> 100/100

Advance through sustained thresholds using >=60 s common intervals and multiple consecutive runs. Report single-stream and multi-stream results separately.

## 8. Provider/fault test interface

`TestLoadedApplicationProductionHarness` supports these environment variables in field1:

- `ITOM_LONG_LOAD=1`
- `ITOM_LOAD_ACCOUNTS=1..10`
- `ITOM_LOAD_USERS=1..2`
- `ITOM_LOAD_STREAMS=1..8`
- `ITOM_LOAD_SECONDS=5..180`
- `ITOM_LOAD_WARMUP_SECONDS=1..30`
- `ITOM_LOAD_COMMAND_DELAY_MS=0..5000`
- `ITOM_LOAD_DIAGNOSTICS=0|1`
- `ITOM_LOAD_TRACE=0|1`
- `ITOM_LOAD_MIN_MBPS=<qualification threshold>`
- `ITOM_LOAD_MAX_RTT_MS=<qualification threshold>`
- `ITOM_LOAD_PROVIDER_MBPS=<fixed synthetic provider byte-rate cap>`
- `ITOM_LOAD_PROVIDER_PROFILE=<time-varying profile; inspect parser/tests for exact syntax>`
- `ITOM_LOAD_FAULT_SEED=<deterministic seed>`
- `ITOM_LOAD_RANDOM_DROP_MIN_MS=<minimum inter-drop time>`
- `ITOM_LOAD_RANDOM_DROP_MAX_MS=<maximum inter-drop time>`
- `ITOM_LOAD_ACCOUNT_OUTAGE=<account/start/duration profile; inspect parser/tests for exact syntax>`

Fault/cap modeling wraps real test IMAP TCP/TLS connections. It does not replace the ITOM DATA path with a mathematical throughput model.

## 9. Suggested Codex workflow

1. Verify hashes and unpack `transport-s1-perf5-field1`.
2. Read this handoff, the development plan, `TRANSPORT_CONCEPT.md`, `TRANSPORT_S1.md`, and the field1-vs-gate19 patch.
3. Run offline `gofmt`, non-heavy tests, `go vet`, then one baseline heavy pair before making changes.
4. Reproduce one P0 failure with the smallest deterministic profile possible.
5. Instrument exact wait ownership/progress; avoid broad timer/limit changes.
6. Make **one causal fix per revision** with a focused regression.
7. Re-run baseline heavy tests to ensure 30/30 behavior did not regress.
8. Re-run the production/fault profile that originally demonstrated the issue.
9. Only after stability improves, do longer capacity tests.
10. Do not re-enable the latency governor during the first stability pass.

## 10. Offline Linux/Codex test setup

Assets include `go1.27.1.linuxamd64.tar.gz` and `itom-gomod-cache.tar.gz`.

Example:

```bash
mkdir -p /tmp/itom-go /tmp/itom-modcache
tar -xzf go1.27.1.linuxamd64.tar.gz -C /tmp/itom-go --strip-components=1
tar -xzf itom-gomod-cache.tar.gz -C /tmp/itom-modcache
export PATH=/tmp/itom-go/bin:$PATH
export GOMODCACHE=/tmp/itom-modcache
export GOPROXY=off
export GOSUMDB=off
cd <extracted-source>/itom/core
gofmt -l .
go test -count=1 ./...
go vet ./...
```

If the cache tar expands with an extra directory level, point `GOMODCACHE` at the directory that directly contains `github.com`, `golang.org`, `go.etcd.io`, `gvisor.dev` and `cache`.

Heavy regression tests:

```bash
go test -count=1 ./tunnel -run '^TestLoadedApplicationRTT$' -v
go test -count=1 ./tunnel -run '^TestLoadedApplicationUploadWithTrace$' -v
```

Production-path smoke:

```bash
ITOM_LONG_LOAD=1 \
ITOM_LOAD_ACCOUNTS=4 \
ITOM_LOAD_USERS=1 \
ITOM_LOAD_STREAMS=4 \
ITOM_LOAD_WARMUP_SECONDS=3 \
ITOM_LOAD_SECONDS=5 \
ITOM_LOAD_COMMAND_DELAY_MS=80 \
ITOM_LOAD_DIAGNOSTICS=1 \
go test -count=1 ./tunnel -run '^TestLoadedApplicationProductionHarness$' -v
```

## 11. Authoritative Windows build for field testing

The source ZIP is executable through Python and extracts to a fresh tree.

```powershell
python .\ITOM_PHASE4_SOURCE_0.9.0-dev1_transport-s1-perf5-field1.zip all --expect-revision transport-s1-perf5-field1 --accept-android-licenses
```

Use the APK and Linux binary produced by the **same successful `all` run**. Do not mix binaries from different source hashes.

After uploading that Linux binary to `/tmp/itom-linux-amd64` on the VPS:

```bash
chmod 755 /tmp/itom-linux-amd64 && sudo /tmp/itom-linux-amd64 install
```

The field1 archive has not been declared Android/Windows-qualified in this Linux analysis environment. The user's Windows `all` run remains authoritative for Android compile/tests/lint/package.

## 12. Real field-test order

For the first Android/VPS test of field1:

1. diagnostics OFF;
2. verify tunnel starts from idle and normal browsing works;
3. test one moderate upload and download separately;
4. test simultaneous load while continuously checking small application requests/echo;
5. record whether starting speedtest from idle causes a collapse;
6. record carrier/account count and any reconnects;
7. if a stall occurs, enable diagnostics for a short reproduction rather than leaving tracing on for the whole field session;
8. preserve Android and VPS logs with the exact source ZIP SHA-256;
9. do not interpret a short >100 Mbit/s burst as sustained capacity;
10. do not manually purge mail during the measurement unless testing recovery separately, because that can hide cleanup inability.

## 13. Historical context that must not be lost

- gate13 was the first stable field prototype: roughly 25–50 Mbit/s download, 3–10 Mbit/s upload, loaded RTT roughly 600–1000 ms; tunnel did not fully die under the reported field use.
- gate18 fixed important latency/reliability behavior but synthetic full duplex remained around 24–28 Mbit/s/direction; upload-only was ~42.5 Mbit/s.
- gate19 introduced the production-path measurement harness and richer diagnostics without intentionally changing transport semantics.
- subsequent performance work identified cleanup starvation, grant replacement/headroom issues and batch collector behavior, leading to the stable field1 runtime.
- short production bursts over 100/100 have been observed; sustained 100/100 has **not** been demonstrated.
- a latency governor prototype was developed, including a 10 Mbit/s floor and 9–11 Mbit/s hysteresis ideas, then deliberately paused because transport progress/recovery problems are more fundamental.

## 14. Things Codex should explicitly avoid

- Do not start from gate51/gate53 experimental runtime instead of field1.
- Do not silently re-enable `docs/experimental/*.disabled`.
- Do not weaken the old 30 Mbit/s or 700 ms regression thresholds to make tests green.
- Do not increase watchdogs as a stall workaround.
- Do not mark logical RECEIPT or age/TTL as physical deletion proof.
- Do not add unlimited speculative grants/credits/connections.
- Do not assume 10 provider accounts scale linearly.
- Do not claim 100/100 from a 2–5 second burst.
- Do not optimize only synthetic memory queues; preserve tests where application TCP payload traverses the real tunnel and IMAP transport.
- Do not make a large multi-hypothesis revision. Keep causal changes reviewable.

## 15. Definition of the next successful milestone

Before pursuing 80/80 or 100/100 again, produce a revision that:

- keeps the existing correctness invariants;
- has no reproducible long-run progress stall in the chosen production profile;
- survives a deterministic one-account outage while sibling carriers continue useful TCP delivery;
- reconnects the recovered account without collapsing the logical session;
- passes the old upload-only gate 5/5;
- passes the bidirectional >=30 Mbit/s, RTT <700 ms gate 5/5 again;
- publishes exact wait/debt/recovery evidence explaining the fixed bottleneck;
- remains buildable as one synchronized Android/VPS revision.

Only then resume 50/50 -> 80/80 -> 100/100 scaling and latency-governor integration.

## 16. Exact field1 ZIP verification completed after packaging

After the revision metadata and this handoff were added, the exact executable source ZIP was unpacked and checked again.

- exact-ZIP launcher `check`: PASS — 34 launcher tests, 25 core-tool tests, offline source checks; 56 Kotlin test cases detected but not executed by `check`;
- exact extracted ZIP `gofmt`: PASS;
- exact extracted ZIP non-heavy `core/tunnel` suite: PASS;
- exact extracted ZIP `go vet ./...`: PASS;
- exact extracted ZIP bidirectional heavy smoke: **42.176 Mbit/s per direction**, loaded RTT p50 **406 ms**, max **477 ms**, PASS;
- exact extracted ZIP upload-only heavy smoke: **41.563 Mbit/s**, loaded RTT p50 **380 ms**, max **512 ms**, PASS.

The Windows `all` build is still required to qualify Android compilation/tests/lint/package and to create the synchronized field APK/Linux binary pair.
