# ITOM field2 project handoff

Current: **transport-s1-perf5-field2**, version 0.9.0-dev1. Development candidate implementing grouped block 1. Base source ZIP: field1 SHA-256 `7d36599b175f2a1e95f52d0e4db453c73a52c93c0ea5d24c389715edc8fb1a34`. Start here and in `DEVELOPMENT_PLAN_GROUPED.md`; historical details are retained in `PROJECT_HANDOFF_FIELD1.md`.

## Implemented

- Strict `journal.Open` remains exact. `OpenCompatible` adds an explicit caller allowlist under the exclusive DB lock. Tunnel DATA accepts exact 8/128 or 12/192 with matching provider/account/user/folders/metadata limits.
- Existing journals retain limits and all accounting. Effective profile is logged on fresh/existing startup. Local bulk/remote offers preserve fast headroom for the actual profile.
- Offline `Store.MigrateConfig` atomically audits/switches capacity and a versioned migration record. `journal-profile --to=data8|data12` is the explicit VPS path. It rejects live/admitted migration and unsafe downgrade.
- T01 includes real gate19-generated DBs in `core/tunnel/testdata/gate19`, with generator/provenance and reference JSON. Focused tests cover dirty states, remote lease, foreign configs, fencing, death/ACK loss and corruption.
- Production payloads distinguish session/user/direction/stream and offset. Each stream TCP destination independently knows expected identity. Early EOF/RESET and target errors fail active load; verified bytes only are counted.
- Measurement uses independent probes and fixed sampler; summary is printed before drain; nearest-rank quantiles, timeout/missed results, bounded failure evidence and explicit phase deadlines.
- A new fixture runs real `StartClientWithOptions` plus Serve's production supervisors via an internal injected dial. Runtime worker lifecycle, recovery, janitor, backoff and carrier epochs are shared with production.
- Provider-test Close interrupts rate pacing. Launcher explicitly reports named optional skipped tests while rejecting failures and other skips.

## Product limitations remain

Owner socket survival is NOT implemented. Its explicit product gate fails on account loss. A repeated non-owner outage can also cause a closed socket or timeout (first trial PASS, subsequent ordinary/race trials FAIL). No-fault supervisor lifecycle is mandatory; fault gates are opt-in and their failures are preserved in evidence. These are qualifications for block 2, not silently accepted release regressions.

The 4a/60s exploratory measurement reached 25.297/47.339 useful Mbps, RTT max 750 ms. It is below future ≥30/30/<700 production criteria. No transport scheduling/window/cap/writer/cleanup/governor tuning was added to hide this result.

ITH remains 9 with DATA batch + striped DATA capabilities. Governor stays OFF in `docs/experimental/*.disabled`. The 25-second authenticated watchdog is unchanged. No refund from RECEIPT/WINDOW/TTL/Closed or goroutine exit.

## Test entry points

Use Go 1.27.1 and the supplied offline cache from the original handoff. Normal `go test ./...` runs core and legacy heavy tests plus no-fault supervisor fixture. Production load is explicitly opt-in. For recovery qualification:

```bash
ITOM_SUPERVISOR_FAULTS=1 go test -count=1 ./tunnel -run '^TestLoadedApplicationSupervisorNonOwnerRejoin$' -v -timeout=120s
ITOM_OWNER_SURVIVAL=1 go test -count=1 ./tunnel -run '^TestLoadedApplicationSupervisorOwnerSurvival$' -v -timeout=120s
```

The second is currently RED; the first is not reliable across repeats. Do not present their default skips as successful recovery qualification.

Load options retain previous flags and add `ITOM_LOAD_PROBE_DEADLINE_MS` (default 1000) and `ITOM_LOAD_MAX_GAP_SECONDS` (default 5). Diagnostics defaults OFF. Speed/RTT qualification thresholds remain explicit flags; omitted thresholds mean exploratory integrity/progress measurement.

External field2 report bundle contains exact ZIP SHA-256, all executed logs including failed/intermediate trials, environment, patch and final validation. The source archive cannot include its own hash; use the report's manifest. Historical report directories inside source are provenance and do not qualify field2.

## Next block

Implement correlated progress evidence and causal fix, logical owner/control survival, then repeated account rejoin (R06–R10 grouped). Retain the fault regressions and replace their RED status with real same-socket exact-byte delivery, not test deletion/relaxed deadlines. See the grouped plan for block 3 provider/proof/fairness and block 4 Android/device/soak/governor.

## Building

The executable source ZIP runs the existing Windows `all` workflow. Use its APK and Linux binary from the same successful build. Linux checks here do not qualify Windows, Gradle/Kotlin/lint, APK/device or a live provider. See `JOURNAL_PROFILE_UPGRADE.md` before an explicit capacity migration/rollback.
