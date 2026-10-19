# ITOM RC10 handoff

Branch: `codex/v1.0.0-rc10-development` in `Septonious/itom`. Source: `archives/1.0.0-rc10/source`. Runtime `itom-1-0-0-rc10`; Android `org.itom.client`, version code 10009. RC10 is under development; do not label compilation or a selected favorable run as release qualification.

The production baseline is exact tagged RC2 with the reviewed descriptor replay/live-index correction, bounded descriptor headroom, first-useful-page unhinted DATA reclaim, observation-only diagnostics, UI/identity updates and exact same-snapshot scan preallocation. Quota, journal schema, wallet, unknown APPEND debt, physical proof/refund rules, crypto identity and RC2 transport policy remain authoritative.

Earlier framing, tighter pressure/escape, adaptive cleanup concurrency, short-recovery and loaded-calibration bundles were not integrated. Their failures and isolated scripts are retained in the outer archive's audit and experiments directories. A missing remote-cleanup fairness yield was found in a speculative cleanup-skip experiment; the experiment still failed broader legacy recovery after restoring that yield and remains outside candidate source.

## Current test model

The user confirmed a healthy eight-core server with about 20 ms RTT to Rambler and good uplinks. Phone-to-provider RTT is normally 20–50 ms and rarely exceeds 100 ms. Primary performance tests therefore use independent server/client TLS access paths: server 20 ms and assumed 1000 Mbps, client 40 ms and 600 Mbps reference, plus 20/50/100 ms client sensitivity cases. The provider's 2 ms processing parameter is an unmeasured scenario assumption, not inferred from ping. See [the full model](docs/rc10/HEALTHY_SERVER_REFERENCE.md).

The previous combined provider-wide bandwidth cap and fixed 80 ms command delays are legacy stress inputs. The all-account 30-second blackout and replica-delay tests remain optional in a separate manual workflow. The primary client-only interruption test keeps the server path reachable and checks original TCP/session recovery. Unit and race checks for quota, unknown outcomes, proof and identity remain mandatory.

The simulator shares its host CPU between client, server and provider. It records actual runtime resources, removes the runner helper's forced two-core default and supports the configured `ITOM_QUALIFICATION_RUNNER` repository variable. It does not emulate or certify a dedicated eight-core server. Do not substitute a GOMAXPROCS value for measured hardware.

## Release checks

The unchanged CI floor is 75.000 Mbps in both directions over full and trailing 30/10-second windows, max loaded RTT <=750 ms, every scheduled probe with no misses/timeouts, useful duplex progress every second, original TCP identity and original/fresh post-load requests. Strict low-client-capacity cases retain latency/continuity gates without claiming impossible throughput. The CLI's default performance target is 100 Mbps with no speed tolerance. No threshold was lowered when changing the environment model.

Fresh and retained journals must both pass. Keep historical reservations charged, including after app reinstall or lost client wallet. Source, Go vet, targeted functional/race checks, Android domain/app tests, lint, JNI/APK identity and exact binary revision precede packaging. Preserve and compare the installed APK certificate; signing-secret names were requested but no prior key has been confirmed. Do not clear state to install an APK with a different signer.

The prior combined-model source checkpoint passed core correctness/recovery/race and some SINGLE runs but failed retained tails and low-capacity startup. Those historical results do not certify the new profile. Record the actual new workflow commit and complete results before packaging.

## Explicit field-test build request

After the paired comparison, the user requested development and packaging RC10 for testing. The separate `itom-rc10-field-test-build.yml` requires source/vet, focused core/proof/recovery/race checks and the unchanged Android SDK/app/domain/lint/JNI gates, then creates a testing package. The strict release-qualification workflow is unchanged and may remain red; no test failure is converted into a pass. The packaged notes state the unresolved low-capacity and100ms-client throughput issues.

Final selection: exact production core from checkpoint522d7cf, used for the paired RC2 comparison. A subsequent stack-copy/typed-sort allocation experiment passed equivalence and microbenchmarks, but another complete qualification run showed intermittent probe/progress stalls. Causality was not established; conservatively retain the earlier production core. The experiment remains in the outer archive only. No candidate policy, quota, journal, proof, recovery or wire identity changes follow that paired comparison.
