# Post-RC2 evaluation and disposition — 20 October 2026

RC2 is the user-selected final 1.0.0 transport. RC3–RC13 were development experiments, not a demonstrated improvement on the user's real network. No later transport change is promoted into final 1.0.0, including plausible correctness fixes. They remain explicitly deferred rather than silently called harmful or deleted.

POST_RC2_CHANGES.zip preserves exact git binary patches from RC2 to each archived RC3–RC13 source (including RC10.1), per-file SHA-256 manifests, historical audits/raw logs and workflow/tooling changes. RC3/RC4 have separate tag histories and are included explicitly. Historical branches/tags and original APK/binary artifacts are retained. This archive preserves code/evidence; it does not include secret signing material or duplicate historical executable binaries. The CI preservation tool can regenerate it from the recorded immutable Git objects. SOURCE_EVALUATION.json lists every path changed in any snapshot, including changes later reverted.

## What the evidence actually says

- RC3/RC4 traded improved ping for field speed loss, failures and reconnects, as reported by the user. Reject their latency policy as a default; do not explain away a field regression using an unrelated synthetic pass.
- RC5–RC9 included larger/faster growth, smaller windows, batching, read gates and placement experiments. Historical ledger records many failures: a 4MiB window gave roughly41/32Mbps despite lower RTT; authenticated-rate admission gave roughly19/18Mbps. Those trades are unacceptable. Bigger384KiB batches failed startup.
- RC10's matched healthy-server comparison was mixed: fresh four-stream mean RTT203ms versus RC2162ms, rates168/221 versus176/232Mbps; fresh MULTI improved, retained MULTI traded download for upload. No uniform improvement was shown. Both original RC2 and RC10 have low-capacity startup/high-client-RTT limitations.
- RC10.1 profiler can attribute queue, fsync, publication and ACK residence. Observation is useful but not itself optimization, and diagnostics overhead must be controlled.
- RC11 duplicate-report no-op commits and RC12 grant changes address measured journal/retained-state cases. They require separate proof and workload controls before reuse; no wholesale journal rewrite is justified by a single throughput result.
- RC13 exact retained-debt reproduction restored20/20 directions instead of14/20 without erasing charges. Its longer synthetic76.7/35.5Mbps versus RC1266.2/30.4Mbps was a single pair, and the severe-reserve branch was inactive in that throughput case. It is not causal evidence for an RTT/throughput advantage over RC2.
- Reinstall/clear-data lost client wallets. Keep this separate from transport performance; neither a new server nor rollback can manufacture proof for missing wallets. Never reset debt to make a benchmark pass.

## Disposition

| Area | Decision and reason |
| --- | --- |
| `core/journal/commit_metrics.go` | Defer no-op transaction telemetry to 1.1; counters are useful but are not an RTT improvement by themselves. |
| `core/journal/ledger.go` | Defer validated duplicate-proof no-op and sparse allocation changes. Potential fsync/GC benefit, but re-prove exact authorization and transaction/cache rollback boundaries first. |
| `core/journal/remote.go` | Defer duplicate-report no-op; correctness tests show preserved validation, not a field performance win. |
| `core/journal/store.go` | Defer errNoChange transaction handling. No changes to RC2 durable storage semantics in final. |
| `core/tunnel/cleanup.go` | Defer first-useful-page reclaim. Red/green proof tests support a specific correction, but no uniform field RTT benefit. |
| `core/tunnel/lane.go` | Defer reclaimed-ordinal cache exclusion, descriptor headroom, bounded profiler and fast-grant refill. These repair specific replay cases but change hot-path work/lease concurrency. Reintroduce individually only after RC2-controlled performance and retained-state tests. |
| `core/tunnel/grant_recovery.go` | Defer smaller fast leases, refill cushion and adaptive historical reconciliation retry. Missing-wallet tests support availability; they do not prove lower idle RTT, and altered recovery proportions must not consume bulk headroom. |
| `core/tunnel/recovered_shares.go` | Defer severe-debt 128KiB reserve policy. RC13 reproduces/restores exact 14/20-lane case; that is a narrow bootstrap fix. It is not a general throughput/RTT optimization. |
| `core/tunnel/delivery_control.go` | Defer ACK residence/profiler changes. Earlier 8/4MiB admission caps and asymmetric experiments are rejected as universal policies: lower lab RTT came with major rate losses. Final uses RC2 window/growth unchanged. |
| `core/tunnel/delivery_policy.go` | Restore RC2. Reject unconditional small-window growth doubling and reduced/asymmetric ceilings without repeated non-regression evidence. |
| `core/tunnel/service_policy.go` | Restore RC2. Reject accelerated first-receipt/warm-resume batching as default; no repeatable field advantage and burst-pressure risk. |
| `core/tunnel/reader_reserve.go` | Restore RC2. Reject indiscriminate accelerated polling/expanded hot windows; demand-scoped adaptive scheduling remains a new experiment, not a cherry-pick. |
| `core/tunnel/io.go` | Restore RC2. Reject global read semaphore, larger cleanup read budget and prior polling changes: they coupled maintenance to useful reads or lacked paired evidence. |
| `core/tunnel/stripe.go` | Restore RC2. Reject role-window overrides and global read gating; protect per-stream independence and useful throughput. |
| `core/tunnel/cleanup_service.go` | Restore RC2. Defer global cleanup gating; no proved cross-workload advantage. |
| `core/tunnel/carrier_feedback.go` | Restore RC2. Defer quiet/spike scoring: no repeated matched evidence, high risk of chasing noisy measurements. |
| `core/tunnel/local_selection.go` | Restore RC2. Keep account-stall routing; defer additional spike scores until placement is measured at batch granularity. |
| `core/tunnel/control.go` | Restore RC2. Defer latest-STATUS telemetry and recovery reserve hookup. No control-delay/window changes in final. |
| `core/tunnel/flow_control.go` | Restore RC2 cumulative coalescing; defer extra timestamp only. |
| `core/tunnel/topology.go` | Restore RC2. Defer FETCH payload/callback histograms pending overhead controls. |
| `core/tunnel/diagnostics.go` | Restore RC2. Preserve newer observation schemas in archive for a bounded, opt-in 1.1 profiler. |
| `core/tunnel/latency_profile.go` | Defer bounded per-stage profiler. Useful for causal attribution, but requires enabled/disabled overhead and allocation measurements. |
| `core/tunnel/trace.go` | Restore RC2. Monotonic timing is worth redesigning into 1.1 observability, not evidence of a transport gain. |
| `core/tunnel/version.go` | Release identity string only, retargeted to itom-1-0-0-final. |
| `core/cmd/itom/main.go` | Release identity string only, retargeted to itom-1-0-0-final. |
| `core/manager/trace.go` | Release identity string only, retargeted to itom-1-0-0-final. |
| `core/mailbox/idle_ahead_test.go` | Retain fixture-only parent deadline correction: 4s parent could not test the production 8s watchdog. Production mailbox code and all throughput/latency qualification thresholds unchanged. |

Android runtime/UI are RC2. The sole packaging exceptions preserve the current org.itom.client identity, versionCode10014, version1.0.0 and signing/build safeguards. Final core differences are three release strings; one mailbox test correction does not change production. Qualification scripts are byte-identical to RC2. A new final release label is a user decision, not an invented passing result for unresolved historical qualification cases.
