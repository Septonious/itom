# RC7 16/12 MiB reconstruction — full strict verification, 2026-10-18

**Decision: REJECT FOR RELEASE. Test-only development artifact is available.** No `v1.0.0-rc7` stable/prerelease tag has been created.

## Exact tested source / artifact provenance

- Tested production-source commit: `2b08127305530b2e16ee3c05f669ad8331a6d559`, branch `codex/rc7-16x12-recovery`; CI run https://github.com/Septonious/itom/actions/runs/39116058706
- Recovered high-speed model: **server 16 MiB**, **client 12 MiB** logical in-flight DATA, RC5 accelerated ACK and BODY-batch policies, RC6 quota and journal protections unchanged. Source is a carefully identified reconstruction, not claimed bit-identical to the historical uncommitted 141.8/79.4 Mbps experiment.
- Android package ID exact `org.itom.client`, no suffix, versionCode 10006. APK compiled and validated; Linux AMD64/ARM64 compiled. Android app signer may differ from any installed `org.itom.client` package; compare signer before upgrading.
- Archive: `ITOM-1.0.0-RC7-16x12-UNQUALIFIED.zip` built by https://github.com/Septonious/itom/actions/runs/39116932479 from the **same exact source** (packaging-only workflow changes verified by `git diff`). Contains APK, two Linux executables, source snapshot, SHA256SUMS, original CI performance evidence, README and change ledger. **Installation is testing-only**.

## Hard 75/75 Mbps, maximum 750 ms loaded RTT: actual results

| Scenario | Upload Mbps | Download Mbps | Max loaded RTT | Acceptance |
|---|---:|---:|---:|---|
| 4 streams, run 1 | 154.368 | 74.720 | 682.909 ms | FAIL download |
| 4 streams, run 2 | 159.349 | 74.650 | 366.751 ms | FAIL download |
| 4 streams, run 3 | 160.965 | 73.387 | 508.669 ms | FAIL download; trailing 10s download 69.783 |
| 32 streams, run 1 | 130.058 | 80.159 | 453.270 ms | PASS |
| 32 streams, run 2 | 125.554 | 77.883 | 439.045 ms | PASS |
| 32 streams, run 3 | 121.416 | 79.320 | 431.011 ms | FAIL trailing 10s upload 70.936 |

All six high-speed rounds completed with 30/30 overlapping RTT probes, no timeout and original TCP sockets preserved. Their numerical overall speed is **not** license to relax the 75/75 trailing-window rule.

## 12-case fresh/retained matrix, independent 60s runs, 10 IMAP accounts

Every fresh and retained case had **30/30 RTT probes with no misses**, but **all 12 failed the release throughput floor**, mostly insufficient download.

| State | Mode | Three runs upload Mbps | Three runs download Mbps | Result |
|---|---|---|---|---|
| Fresh | Single | 145.721 / 146.538 / 145.861 | 72.911 / 72.859 / 73.099 | 3 FAIL |
| Fresh | Multi | 131.081 / 132.557 / 130.701 | 70.879 / 68.529 / 69.700 | 3 FAIL |
| Retained 6MB + 130 messages/account | Single | 78.578 / 81.217 / 82.130 | 67.812 / 70.311 / 70.875 | 3 FAIL |
| Retained 6MB + 130 messages/account | Multi | 82.536 / 81.802 / 81.675 | 64.898 / 63.649 / 64.300 | 3 FAIL |

## Startup and downgrade

- True 10→20→50→150 Mbps: timeout of first loaded RTT probe at 10 Mbps startup, ~2.5 s into measurement; FAIL.
- True 20→50→150 Mbps: completed 60s, 30/30 probes, but **max loaded RTT 1,419ms**; FAIL.
- 150→50→20→10 Mbps downgrade: RTT probe timeout after 22.5 s, at the 20 Mbps phase; FAIL.
- The stage tests use `ITOM_LOAD_PROVIDER_PROFILE_EARLY=1` to begin at the initial simulated link cap *before* opening the application sockets. They don't confuse an abrupt high→low drop with an upward speedup.

## Correctness, build and open blockers

- Android pinned SDK37/Kotlin/tests/lint/JNI both ABIs: PASS. Linux AMD64/ARM64: PASS. Targeted 16/12 policy/controller tests: PASS.
- Full Go `go test ./...`: **FAIL** on `TestFieldLegacyGrantPublicationDoesNotStrandThreeAccounts`; a readiness test found `a06` with no currently available bulk/fast grants. Repeating the test locally (five runs) reproduced the event for `a08`. Some transient grant depletion is allowed by production's ready-state semantics, but this requires a *separate bounded recovery/availability proof*—do not silently weaken the test.
- Next engineering priority: preserve high-speed 16/12 mode; improve server→client throughput by at least 5–17%, especially retained journals; ensure 32-stream trailing upload ≥75; fix/qualify grant readiness and low-capacity latency without compromising durable quota. Run same matrix after each modification.

**Release contract:** `org.itom.client`, at least **75/75 Mbps** (full 60s and trailing windows), maximum loaded RTT **≤750ms**, 30/30 probes, 4+32 streams, fresh+retained 12 cases, no startup/blackout/stale-grant failures, full Go/race and Android checks, then real phone/provider qualification. Until every gate passes, **NO PROPER RC RELEASE**.
