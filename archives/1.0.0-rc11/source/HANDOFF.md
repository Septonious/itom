# ITOM 1.0.0 RC11 — field-testing candidate

Baseline: packaged RC10.1, source commit 76d08dc80a23f9322350ccdc570bad86b5768df6. Android identity remains org.itom.client, version code 10011. Update both client and server for matched diagnostics.

## Changes

Fully validated duplicate journal observations and remote quota reports no longer fsync unchanged state. Changed transactions still fsync atomically; forged evidence, mixed invalid batches and lost commit acknowledgements retain their original fail-closed behavior. Health exposes a separate Noop counter. Historical grant offers retry at 15/30/60/120/240/300 seconds, capped at five minutes, resetting on durable progress. Unresolved historical debt remains charged.

Wire format, physical quota profiles, flight/window limits, bulk service policy, journals, wallets and recovery authority are preserved. No qualification threshold is reduced.

## Focused evidence

Local Go vet; journal/wallet/flow/datapath suites; selected delivery, cancellation, delayed-prefix, reader-reserve and profiler regressions; focused journal/recovery race tests; 91 Python tests; source identity/integrity checks passed.

The same new home-path regression ran on RC10.1 and RC11: healthy 10 ms / 500 Mbps client path, 20 ms / 1 Gbps server path, actual TLS/IMAP and fsync, four accounts retaining 6 MiB/130 old remote slots each without matching historical wallet, three rounds of 24 startup streams, verified transfers and short requests under ramp-up. Both passed all 72 streams and subsequent tails/transfers. In one run per version, the twelve short exchanges averaged RC10.1 192.1 ms (maximum 452 ms), RC11 152.1 ms (maximum 218 ms). These are small synthetic samples on a shared two-CPU test process, not a statistically established speed or RTT improvement, an eight-core deployment model, or real Speedtest qualification.

Field logs show repeated historical recovery requests and server journal/selection delays, with all 65 retained target dials successful. The exact trigger of each immediate Speedtest failure remains unproven. Real-provider repeatability, sustained throughput and sub-200 ms RTT require field validation; RC11 is not declared a stable release.

## Build

Run `python3 tools/build.py check --expect-revision itom-1-0-0-rc11` for source checks. The repository workflow `.github/workflows/itom-rc11-field-test-build.yml` builds matched APK, Linux amd64/arm64 binaries, source and a SHA256 handoff after focused checks. Full qualification tools and their thresholds remain available unchanged.

Keep existing server state, quota journals, client wallets and identities. Do not clear application data or journals to resolve a signing/update mismatch. The debug APK signer is recorded with the build; compatibility with an installed debug certificate is not assumed.
