# ITOM 1.1.0 DEV5 testing RC

Current source/ contains DEV5 shared-socket fairness, bounded historical-grant audit traffic, dedicated-reader response search and faster useful-ACK startup. Stable1.0.0/RC2 remains unchanged. DEV4 is frozen in checkpoints/DEV4/source.

[Testing notes and downloads](DEV5_TESTING.md) | [Build checks and hashes](DEV5_BUILD.json) | [Matched DEV4/RC2 results](experiments/field-feedback/RESULTS.md) | [Field analysis](experiments/field-feedback/FIELD_ANALYSIS.md).

Direct comparison:48/48short phases versus DEV4 42/48; original sustained17/20 versus RC2 13/20. Full Go/vet/races and all89Android unit tests pass. Known capped MULTI download and idle-tail regressions remain; real Rambler improvement is unverified. Thresholds are unchanged. Preserve journals, wallet data and signing identity between updates. The workspace persistent APK signer differs from the temporary CI debug signer; see testing notes before choosing a package.
