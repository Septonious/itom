# ITOM1.1.0 DEV3 development

The current source/ is the tested experimental grant-aware delivery model. Final1.0.0/RC2 is unchanged. This is a field-testing build, not a stable or fully performance-qualified1.1.0 release. Android remains org.itom.client, versionCode10103.

The new controller separates sparse scheduling from sustained bulk admission. Bulk flight grows from authenticated useful delivery and is not repeatedly halved on RTT/short-ACK age alone. Physical supply waits are distinct from admitted network/service work. Confirmed capacity decline and genuine stalls remain bounded; drained bulk returns to its startup seed. Journal, wallet, pool, wire, quota and unknown-outcome proof authority remain the RC2 contracts.

[Results, failures and downloads](RESULTS.md) include matched RC2 comparisons, changing40–180ms mobile paths, retained state, provider-service sensitivity and full-flight demand transitions. Complete Go/vet/race suites, worst4G and Android SDK checks pass. Some sparse RTT tails still worsen, retained upload still misses100Mbps for both RC2 and the model, and real Android/Rambler qualification remains outstanding. No threshold is relaxed.

- [APK testing artifact](https://github.com/Septonious/itom/actions/runs/39628803015/artifacts/12360365671)
- [Linux amd64/arm64 testing artifact](https://github.com/Septonious/itom/actions/runs/39628803015/artifacts/12358599228)
- [Build metadata and hashes](DEV3_BUILD.json)
- [Controller design and reproducible experiments](experiments/delivery-model/README.md)
- [Migration/data preservation](MIGRATION.md)

The APK uses a temporary debug signer because persistent Actions signing secrets were absent. In-place update compatibility is not assured; do not clear wallet data to work around it.

The source files match the staged DEV3 build recipe at922545c0fe3860693fc9a4c95e9cee73145cef69; Linux amd64 was independently reproduced bit-for-bit. The frozen [DEV2 checkpoint](checkpoints/DEV2/README.md) supplies immutable inputs to the experiment recipes, avoiding recursive application of the model. [Development history](DEVELOPMENT_HISTORY.md), earlier results and rejected patches remain available.
