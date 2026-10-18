# ITOM RC2 → RC7 source audit — 2026-10-18

**Baseline:** archived 1.0.0 RC2 (previously relatively stable in field). RC7 remains **unqualified** until required gates pass. The published RC6 tag is not overwritten.

## Immutable user requirements
1. Android debug application ID exactly `org.itom.client.dev.rev7`, namespace `org.itom.client`, Gradle debug suffix `.dev.rev7`. Never build another package ID for an ITOM candidate.
2. Full-capacity release qualification: at least **75.000 Mbps upload AND 75.000 Mbps download**, verified over the whole 60-second duplex measurement, trailing 30-second and trailing 10-second windows, never a peak or averaged-up one-way result.
3. Loaded maximum RTT **<=750 ms** (no tolerance), 30/30 overlapping scheduled probes, zero probe failures, original application sockets survive, no missing or zero-rate duplex seconds; full recovery and correctness suites mandatory.
4. Under deliberate 10-20Mbps provider emulation, 75/75 is physically impossible. These ramp/outage tests must instead enforce continuity, zero error probes and <=750ms loaded RTT. They cannot be represented as passing full-capacity throughput qualification.
5. Optimize above 75/75 and reduce RTT as far as possible, but never trade below either minimum for a release.

## Exact RC2→RC6 differences
- Core tree differs in 19 files, of which 11 are substantive runtime/metadata/diagnostics and 8 are regression tests. Android differs only in manifest/versionCode/versionName (package ID is unchanged).
- RC5 `delivery_policy.go`: added early window **doubling** under favorable authenticated ACKs while flight <4 MiB. Not present in RC2. RC7 candidate reverts policy to RC2 behavior, preserving RC6 delivery core and safety.
- RC5 `service_policy.go`: faster first 32KiB →64KiB BODY ramp and 128KiB warm-resume after 8 prior samples. Not present in RC2. RC7 candidate restores RC2 batch growth.
- RC6 `delivery_control.go`: session bulk max 16MiB →8MiB; newer ACK age telemetry. Retained while testing because returning to a blind 16MiB admission without proving latency is unsafe. Physical journal quota is unchanged.
- RC5/6 `control.go`, `flow_control.go`, `lane.go`: diagnostic timestamp for coalesced latest logical status; does not confer extra credit.
- RC6 `topology.go`/`diagnostics.go`: physical FETCH timing telemetry (observation only).
- RC6 `version.go`, `main.go`, `manager/trace.go`: revision strings.
- FieldFix1: advisory 1s short-request placement to reduce a03 carrier skew; FieldFix2: static 4MiB maximum. Both remain experimental; 32-stream steady lab passed around 40/32Mbps but **under 75/75**, and genuine 10→20→50→150Mbps ramp failed with RTT 1.45s or startup timeout. These must not be shipped as RC7.
- Adaptive field prototype: ~19/18 Mbps, 432ms RTT steady at 150Mbps, still failed ramp and the 75/75 floor. Rejected.

## Correctness and tests
- Use archived RC6 journal/reconnect/account handling and full tests. No changes to identities, account credentials, quota, physical APPEND evidence, retained journal, logical stream identity, or server default.
- Release qualification requires independent CI success on 4-stream and 32-stream 10-account 150Mbps workload at >=75/75 and <=750ms; normal Go integrity/recovery/race tests; matched signed APK and Linux binary build; plus repeated full 12-case fresh/retained state lab and actual Android/Rambler tests.
- An experimental build may be shared as **unqualified**, but do not publish the v1.0.0-rc7 prerelease or call it stable until hard gates pass.
