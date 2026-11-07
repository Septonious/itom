# ITOM · 0.9.0-dev1 · transport-s1-perf5-field3

Field3 is a grouped development candidate for logical owner lifetime, cross-carrier stream controls, durable pre-APPEND retry, repeated owner/non-owner account recovery and correlated debt/grant measurement. ITH9 now requires the new `LOGICAL_CONTROL` capability: build and update Android/VPS together. **Sustained production latency/throughput qualification and device/real-provider checks remain open.** Governor stays off.

Current instructions: [project handoff](docs/PROJECT_HANDOFF.md), [grouped four-block plan](docs/DEVELOPMENT_PLAN_GROUPED.md), [journal upgrade/rollback](docs/JOURNAL_PROFILE_UPGRADE.md). Historical statements below describe earlier milestones; they are not field3 qualification.


> **FIELD-TEST CANDIDATE.** The experimental latency governor is intentionally disabled in the runtime. Its latest prototype is retained under `docs/experimental/*.disabled`. This source keeps the stable grant/cleanup/batching work used for real Android/VPS testing. The legacy 30 Mbps / 700 ms synthetic gate is mostly stable but one of five final bidirectional qualification runs produced a single 715 ms RTT sample, so this candidate is not a release qualification. See `docs/PROJECT_HANDOFF.md` for exact results and known issues.
**PERFORMANCE GATE CANDIDATE — NOT A DEPLOYMENT RELEASE.** Run the full `all` gate before deployment. The current Go acceptance gate requires >=30.0 Mbps directional payload (per direction for bidirectional load) and loaded RTT max <700 ms. Do not weaken these thresholds to make a candidate pass.

Gate18 keeps the field-working gate13/gate15 reliability fixes and gate16/17 IMAP-IDLE, scheduling and compact-diagnostics work, then fixes the remaining IDLE response-latency flaw. Latency-critical local control can now end only the current dedicated-reader IDLE command with IMAP `DONE`, immediately recheck the mailbox, and use a short hot polling window before returning to event-driven IDLE. The authenticated reader socket is not closed or reconnected. Queued wakeups in the FETCH→IDLE race are preserved. To keep the new >=30 Mbps gate stable, bulk cumulative leases retain their 2 MiB byte size and two-lease admission and use the implemented 32-message ordinal budget, avoiding premature grant churn with ~100 KiB batches; total normal remote byte reservation remains 4.5 MiB. ITH9, accounting correctness, cleanup semantics, the 25 s transport watchdog, per-message limits and Android UI behavior are unchanged.


Gate19 is a measurement-only development slice over gate18. It does not change ITH9, transport windows, DATA grant sizes, cleanup semantics, watchdogs, or the existing 30 Mbps / 700 ms acceptance thresholds. It adds an opt-in production-path full-duplex harness (`TestLoadedApplicationProductionHarness`) that uses the shared VPS `accountRuntime`, independent upload/download application streams, a common time interval, continuous application DATA RTT probes, streaming content verification, and profiles for 1–10 accounts, 1–2 users and 1–8 streams per direction. Diagnostics now separate DATA/control APPEND MIME bytes, logical DATA payload/batch fill, and shared-runtime queue/busy/service/per-user byte counters. The harness is exploratory unless explicit `ITOM_LOAD_MIN_MBPS` / `ITOM_LOAD_MAX_RTT_MS` assertions are supplied.

Example production-path measurement:

```powershell
Push-Location .\core; $env:ITOM_LONG_LOAD='1'; $env:ITOM_LOAD_ACCOUNTS='10'; $env:ITOM_LOAD_USERS='1'; $env:ITOM_LOAD_STREAMS='4'; $env:ITOM_LOAD_SECONDS='30'; go test ./tunnel -run TestLoadedApplicationProductionHarness -count=1 -v; Pop-Location
```

Build on Windows PowerShell from the directory containing this executable source ZIP:

```powershell
python .\ITOM_PHASE4_SOURCE_0.9.0-dev1_transport-s1-perf5-field3.zip all --expect-revision transport-s1-perf5-field3 --accept-android-licenses
```

Deploy only after this exact archive passes the full Windows `all` gate. Then use the APK and Linux binary from that same successful build; update Android and VPS together. VPS installation:

```bash
chmod 755 /tmp/itom-linux-amd64 && sudo /tmp/itom-linux-amd64 install && sudo itom
```

Gate11 field-reliability remediation: cross-carrier physical-attempt ownership is retained at LogicalSession scope across carrier reconnects; detach no longer recursively cancels unrelated stream owners; client DATA scheduling reserves a grant-ready healthy carrier rather than selecting one and waiting indefinitely; stale/black-holed OPEN owners are quarantined and retried on another carrier; cleanup traffic has a separate bounded queue and transient cleaner/maintenance failures are deferred/repaired without killing the application lane. Android diagnostics and packet trace are disabled by default and can be toggled explicitly; with diagnostics off, trace files and extended IMAP instrumentation are not created.

`ITH9` / wire version 9 is intentionally incompatible with fix7/ITH8 and all older peers. Fix8 replaces the probe-first boot handshake with a fresh HMAC-authenticated server-first challenge; a signed client probe remains only as fallback for reattaching when an older authenticated lane is still alive. Both nonces remain bound into the Noise prologue, so historical mailbox hellos cannot reactivate a session after a VPS restart. Update Android and VPS together. No automatic wire downgrade is implemented.

Android SDK note: this revision targets API 37; because Android 17 is currently preview, the pinned local sdkmanager install explicitly enables preview channel 3.

Android build provisioning pins Command-line Tools 22.0, Android SDK 37.0, Build Tools 36.0.0, Gradle 9.3.1, and AGP 9.1.1.

Replay/restart note: startup journal recovery may inspect historical mailbox rows once and reports them as `recovery_rx`; live `packet_rx` begins only after the fresh ITH9 challenge/Noise handshake and starts after the accepted handshake UID. Repeated old UIDs are therefore not a valid live-session path.
Fix11 throughput-stability slice: DATA batch `CommitUnknown` is non-fatal in both single and batch writer paths; remote physical quota uses four-message cumulative leases within the unchanged 8 MiB exposure cap; completed grant keys no longer accumulate toward a lifetime 64-grant stall; empty IMAP polling is adaptively paced; and recovery skips stale UIDVALIDITY locators before issuing per-UID absence probes.
Fix12 gate/runtime correction: stale tests now match the fix11 polling policy, the duplicated batch test helper no longer prevents `datapath` from compiling, and cumulative remote DATA binds each authenticated descriptor to its exact physical ordinal instead of requiring ordinal 1 to remain in the journal. ITH remains 9.


Gate7 tail-latency correction: the reserved remote latency grant is now a dedicated 512 KiB / 32-message cumulative lease, while two bulk grants remain 2 MiB / 4 messages each. This prevents tiny interactive DATA from forcing a GC/reissue rollover every fourth physical message, reduces total remote byte reservation from 6 MiB to 4.5 MiB, and keeps ITH9 unchanged.

Gate8 maintenance-race correction: `Present()` now uses a UID-only bounded proof path instead of the general payload FETCH validator. IMAP can legally deliver a concurrent unsolicited `FETCH (UID ... FLAGS ...)` before the tagged completion of a UID FETCH; go-imap may route that row into the pending command when the requested UID was expunged before the solicited row. Gate8 treats such a requested UID conservatively as still present for that GC pass and retries later, so ambiguous maintenance can never release debt. The general FETCH validator remains strict and now reports bounded `IMAP_SCOPE_FETCH_*` subreasons while preserving the external `IMAP_SCOPE` classification. Sparse 40-UID/hole and concurrent STORE/EXPUNGE regressions cover this path. Throughput, windows, batching, fast/bulk lease shapes and ITH9 are unchanged.


Gate9 audit remediation: physical APPEND capacity is no longer held while DATA waits for a storage grant; striped reader wake propagates to every carrier; live writer count bounds physical admission; OPEN completion/cancellation/deadline state is bounded; client logical sessions rotate at the existing 4,096-stream ITH9 lifetime bound; the automatic janitor removes only independently recognizable pre-auth garbage; semantic UIDVALIDITY changes remain retryable without poisoning the mailbox connection; stale carrier epochs cannot replace newer lanes; cleanup status now distinguishes an already-retired missing wallet grant from a real WALLET_STATE divergence; and the load harness continuously watches carrier exits while reporting separate grant and physical-slot wait maxima. ITH9, performance windows/lease shapes and release thresholds are unchanged.
