# ITOM 1.0.0 RC13 — server-side retained-capacity correction

Baseline: packaged RC12 source270b4656168b6298ab9411f2c8a4bfca29f874ee. Android identity remains org.itom.client, version code10013/name1.0.0-rc13-debug. Qualification tools/thresholds, physical profiles, normal grants, proof/journal/recovery authority and wire codec are unchanged.

## What changed

Accounts retaining enough old byte debt to leave less than2MiB unused now use128KiB recovery fast reserves in each direction, rather than512KiB local/256KiB remote.128KiB holds two maximum sealed fast batches including authenticated MIME. This preserves enough genuinely free capacity for a full512KiB bulk BODY in both directions on the three exact affected account states. Normal and less constrained retained accounts keep previous reserves. Every old byte and ordinal stays charged; no missing-wallet refund/reset is introduced.

The early bulk pre-fence refill experiment was rejected after lower measured throughput. Final lane.go is unchanged from RC12. Only server paths in grant_recovery.go, recovered_shares.go and the l.side branch in control.go contain selected transport changes.

## Update without losing wallets

Keep the installed RC12 Android app and existing profile; update the server to RC13. The unchanged RC12 client codec already supports the reduced explicit recovery fast grants. The RC13 APK is available for fresh installations or a compatible signing certificate. Do not uninstall/clear data or import a new profile merely to bypass a signing mismatch. See docs/STATE_AND_SIGNING.md for the confirmed missing-wallet cause and optional persistent Actions signing secrets. No private signing key is stored in source/artifacts.

## Targeted evidence

Exact three-account state regressions fail on RC12 and pass on RC13. A real ten-account TLS supervisor test reaches20/20 lanes, verifies per-account TCP traffic and full bulk/short exchanges on the affected accounts, preserves old grants and passes journal audits.

Same original phased workload: RC2 completed25.491/28.770Mbps but failed the concurrent short-request deadline; RC12 passed at66.029/41.509Mbps. RC2's238 production files are verified unmodified. In the extended128MiB-per-phase pair RC12 passed at66.246/30.425Mbps (short mean/max289/1769ms), RC13 at76.735/35.464Mbps (249/1125ms). Upload sink samples stayed30.199–39.007Mbps for the candidate. These are limited serial shared-three-CPU lab samples, not physical Android/Rambler or eight-core hardware qualification. RTT spikes remain; sustained100Mbps and sub200ms RTT remain unqualified.

Full results, rejected experiment and methods: ../audit/TUNING_COMPARISON.md. No acceptance threshold is relaxed or reported passed on the strength of these targeted cases. The workflow gates APK/server/source outputs with source identity, journal/quota/proof, retained-state, phased TLS, profiler, Android and race checks.
