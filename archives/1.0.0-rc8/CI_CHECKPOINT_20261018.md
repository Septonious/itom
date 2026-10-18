# RC8 development CI checkpoint — 2026-10-18 UTC
**Release status: BLOCKED.** This is a source/UI development checkpoint, not a production build.

## Revision and accepted architecture
Branch: `codex/v1.0.0-rc8-development`, first performance/Android CI tested source commit `6da7ffd07277425a689c772ea25c11c55120e94b`. Later commits add independent test workflow only; runtime source unchanged.
- App ID `org.itom.client`, debug version `1.0.0-rc8-debug`, code 10007. Android SDK37 tests/lint and both JNI ABIs PASS, original APK may need same signing certificate for in-place update.
- Restored 16MiB server / 12MiB client logical flight from RC7 preserved. Journals, IMAP physical byte/message grants, recovery and in-flight TCP identity unchanged.
- New fresh positive RTT-spike penalty (max 240 scheduling score, stale after 10s); only short/interactive carrier selection, never bulk quotas or exclusion. UI uses near-black high-contrast Material3 surface tokens, cyan/violet/green/coral accents, themed admin Sections menu, gapless progress.
- Go source integrity and focused account/carrier regressions PASS in independent CI; local 92 Python tests, Go -race targeted, datapath, flow, journal PASS.

## Measured full-capacity performance (10 accounts, 600Mbps fixture, 60s, 30 probes)
| Logical workload | Upload Mbps | Download Mbps | 10s/tail floor Mbps | Max RTT | Gate |
|---|---:|---:|---:|---:|---|
| 1 stream | 156.819 | 80.011 | **73.531** | 502.096ms | **FAIL** trailing floor |
| 4 streams | 157.291 | 77.394 | **74.973** | 453.224ms | **FAIL** trailing floor (do not round up) |
| 32 streams | 132.453 | 79.194 | 76.494 | 461.912ms | **PASS**, one repeat only |

None of these laboratory rates establishes actual Android/Rambler field performance. All high-capacity trials completed 30/30 probes without timeouts; original TCP sockets stayed alive.

## True capacity-constrained start (created before opening application sockets)
| Scenario | Measured issue | Gate |
|---|---|---|
| 10 Mbps, then ramp 20→50→150 | First cold application RTT probe **timed out** | FAIL |
| 20 Mbps, then ramp 50→150 | Run stopped at 16.5s, maximum 1,123ms, RTT probe timeout | FAIL |

**Next priorities:** fix grant/admission and startup responsiveness under 10/20Mbps, improve single/four-stream 10s tail download without reducing high-capacity throughput, preserve input TCP connection and physical journal integrity, repeat fresh+retained 12-case matrices, verify user Android diagnostics. The isolated low 165ms account RTT screenshot is *not* proof of a 165ms end-to-end service RTT.

## Evidence
- First baseline build/tests + 4/32 strict: https://github.com/Septonious/itom/actions/runs/39124885071
- SINGLE and real 10/20Mbps early ramp: https://github.com/Septonious/itom/actions/runs/39125280145
- Previous RC2→RC7 full experiment ledger: `archives/1.0.0-rc7/RC2_ONWARD_EXPERIMENT_LEDGER.md` in prior development branch.
- Release threshold remains **both upload/download >=75.000Mbps including 60s, last 30s, last 10s, max loaded RTT <=750ms, zero probe failures**. Never waive for full-capacity release tests; physically limited low-capacity ramp checks latency/continuity separately.
