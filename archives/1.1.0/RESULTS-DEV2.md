# ITOM 1.1.0 DEV2: measured checkpoint

Source/build commit: [37d3fae](https://github.com/Septonious/itom/commit/37d3fae1b56213a8c6541e85ad64fa294155142e). [Complete run](https://github.com/Septonious/itom/actions/runs/39602535979). **Experimental; performance qualification failed.** Full correctness, race checks and Android/Linux builds passed. Final 1.0.0/RC2 is unchanged. The new useful-delivery controller is a separate experiment and is not included in these APK/binaries.

## Idle/light application RTT

Ten actual supervisors and account pairs, TLS/IMAP, journals and persistent verified 32-byte application exchanges. Server route 2ms/1Gbps; client 500Mbps. Three alternating runs per variant, 72 requests per phase. Light traffic is a closed-loop 4KiB echo with think time. These are shared-host laboratory results, not Android/Rambler measurements. Mean / p95 / max, milliseconds:

| Client route | RC2 idle | DEV2 idle | RC2 light | DEV2 light |
| --- | ---: | ---: | ---: | ---: |
| 5ms | 48.9 / 76.0 / 117.3 | 36.3 / 63.4 / 71.0 | 52.7 / 112.1 / 150.3 | 36.0 / 62.7 / 87.8 |
| 40ms | 126.1 / 254.9 / 482.3 | 138.6 / 312.1 / 740.7 | 137.4 / 301.6 / 404.6 | 124.3 / 279.7 / 405.4 |
| 100ms | 196.2 / 268.4 / 400.5 | 191.5 / 226.0 / 268.8 | 197.0 / 277.0 / 363.1 | 179.3 / 257.1 / 388.5 |

The 5ms benefit repeats. The 40ms idle mean and tail worsened in this run; it is not a universal latency improvement. Restricting the new receive search to measured <=20ms independent DATA/maintenance paths prevents the prior capped-socket recovery failure, but does not establish tail equivalence on all mobile paths.

## Complete sustained measurements

Two complete 60s rounds per side and scenario. Existing full/30s-tail/10s-tail, complete probe schedule, application/carrier identity, strict 100Mbps and 750ms gates are unchanged. Table rates average complete rounds, upload / download Mbps. Every missed target remains a failure.

| Scenario | RC2 | DEV2 | Historical gate |
| --- | ---: | ---: | --- |
| single | 177.0 / 345.6 | 180.0 / 352.9 | Both PASS |
| four | 160.8 / 297.2 | 158.4 / 299.7 | Both PASS |
| multi | 154.6 / 268.3 | 153.1 / 269.4 | Both PASS |
| binary-four | 238.6 / 256.7 | 241.5 / 256.8 | Both PASS |
| retained-single | 76.3 / 309.1 | 76.9 / 306.7 | Both FAIL 100Mbps upload/tails |
| retained-multi | 76.7 / 313.5 | 76.9 / 311.6 | Both FAIL 100Mbps upload/tails |
| historical-route | 132.0 / 164.6 | 133.2 / 164.2 | Both FAIL; loaded tail / RTT |

The 20ms-server sensitivity produced RC2 max 938ms and DEV2 max 809ms; DEV2 also had an 88.159Mbps minimum verified tail. All 28 measurement rounds completed, with zero probe timeouts/misses in the recorded rounds. Complete rounds and successful sockets do not convert failed rate/RTT gates into passes.

## Correctness and builds

DEV2 full Go suite/vet, required journal/wallet/flow/datapath race checks and worst-4G check passed in CI. The guard also passed five repeated capped/shared-socket repair/controller checks locally without changing their deadlines. The extra local HTTPS race-instrumented 5s timeout recorded in the DEV1 report remains a failure, not a passed check.

Android: 82 domain tests and 7 app tests passed. Lint: zero errors, one dependency-version warning. JNI identities/ABIs and package org.itom.client, versionCode 10102, versionName 1.1.0-dev2-debug were verified. Linux amd64 ran its version command; both Linux files are normal CGO-disabled ELF builds without test-fixture APIs.

Downloads (experimental DEV2 only):

- [APK artifact](https://github.com/Septonious/itom/actions/runs/39602535979/artifacts/12347109176): ITOM-1.1.0-DEV2-debug.apk; SHA256 `82b89db715fc34281b995605267e4a6f28a15df82bbb1c5d2e5f4c90a24cb700`.
- [Linux artifact, amd64 and arm64](https://github.com/Septonious/itom/actions/runs/39602535979/artifacts/12347912728). amd64 SHA256 `ad499b359dbaf46c7cb8dbfd4b7cb00abbf4ff95544b58b00379a5bd1d95d21e`; arm64 `c5abe46dfd75c32d448e0b5c2989df70a7e5be735fbddbdc1279316aa80d4e83`.

**Signing:** the completed build used a temporary debug signer because persistent Actions signing secrets were absent. In-place update compatibility is not assured. Preserve wallet data and server state; see [MIGRATION.md](MIGRATION.md).

Exact raw samples, rounds and log hashes: [ci4-dev2-results.json](experiments/ci4-dev2-results.json). Source file hashes: [SOURCE_INTEGRITY_DEV2.json](experiments/SOURCE_INTEGRITY_DEV2.json). Prior checkpoint failures: [RESULTS-DEV1.md](RESULTS-DEV1.md).

## Follow-up experiments and current priority

Faster quota-aware reclamation raised retained upload but also reduced download. Conservative 900/1000/1100ms floor trials each included four full rounds per variant in reversed orders. Upload averaged 120.4/111.8/103.8Mbps versus controls near 77; download fell approximately 5.7/3.6/2.6%. The earlier faster trial lost substantially more SINGLE download. These changes are preserved separately and **not promoted**. [Exact conservative trials](experiments/reclamation-floor-results.json). The fresh sparse fast-fence comparison did not demonstrate a mean RTT benefit.

The user then revised the controller objective: lowest practical idle/light RTT, bandwidth-first sustained bulk, dynamic 40–180ms mobile turns. The new independent useful-delivery model, invariants, phase transitions and paired RC2/DEV2/model trials are documented in [delivery-model/README.md](experiments/delivery-model/README.md). No DEV2 artifact contains that model.
