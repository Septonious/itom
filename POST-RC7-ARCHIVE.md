# Archived ITOM 1.5.1 RC8–RC15 development

Frozen following RC15 field regression. The active baseline is exact RC7 (598a048b5addeace7ad7c08f3531688ebc8a70be), source tree 34b30831e2f1684cf901b05507fe89926c1bedf0. No later code is approved for automatic reapplication.

This archive retains the last field build tree plus this manifest. Additional commit parents retain divergent experimental checkpoints; original branches are not deleted. Earlier historical commits are preserved through their parent chains. Do not merge this archive into main.

## Archived refs

- `checkpoint/itom-v151-rc14` — `90dd9fa93b3c234d4479fda1c6a8957e18ed836a`
- `checkpoint/itom-v151-rc15-bounded-validation` — `7de7b600aa79b8e45c49ea0be236d7784ac3debe`
- `checkpoint/itom-v151-rc15-cellular-backpressure` — `6df194afd31d4f57b02426ce2d7759b50d7192a8`
- `checkpoint/itom-v151-rc15-combined-recovery` — `6ea2b502dde8d5fd13e7fdc9d5f244a3d9a0090f`
- `checkpoint/itom-v151-rc15-credit-writer-isolation` — `3782eb6de35aebf1569730e7c5adbcc511502abd`
- `checkpoint/itom-v151-rc15-dual-cellular-qualified` — `700f25fb9b481dc46c6d7ee45c8cb6042d35b392`
- `checkpoint/itom-v151-rc15-field-rejected` — `57db36290c4c4ce83356cdb95da5ba3e669fc1f6`
- `checkpoint/itom-v151-rc15-mobile-credit-fragments` — `6f939a6c9c6e43cff424ed22c4f024c228bb023b`
- `checkpoint/itom-v151-rc15-relay-credit-recovery` — `d89030c56a77fdbbb24d80629bf78e204812219a`
- `checkpoint/itom-v151-rc15-reverse-headroom` — `90cb4b6040dc695b9194247ad222213d56cbdcbe`
- `checkpoint/itom-v151-rc15-serial-demand-recovery` — `509fb93bdc72f078f04bc490681f7ca06d115a4e`
- `checkpoint/itom-v151-rc15-sparse-receipts` — `217979fbf21e303fdcd13fb2aacdebdf21b7e6d7`
- `checkpoint/itom-v151-rc15` — `965d7741f790678133f7a465b8018fc940173837`
- `codex/itom-v151-rc8-tail-latency` — `3752f1ca376e95677fd3e4cadafc0fd122c02b7f`
- `codex/itom-v151-rc9` — `6b22815df3afc01e05e5eebfde5cd3f84411bf0b`
- `codex/itom-v151-rc10` — `828f05ca08c86dd35702041345c4a21ddd167201`
- `codex/itom-v151-rc11` — `62c76953da047b836a8f9fdaa07250dcee178bb0`
- `codex/itom-v151-rc12` — `7d100c90ce978aa60b8c897884b8398a72166645`
- `codex/itom-v151-rc13` — `3e85b2c19b32cbbe936656e8592568bf670a0bff`
- `codex/itom-v151-rc14` — `a68f600e2cf6df4b3842a014ab61cbcb6aec80d5`
- `field/itom-v151-rc15` — `da32c103dbc058d5c210dbb5592c9d5a237dbe08`

## Field evidence and priorities

RC15 Android diagnostics and server trace captured 2026-11-27: 70 positive client RTT samples, minimum 128 ms, median 157 ms, maximum 249 ms; zero below 100 ms. 68 of 70 were at least 130 ms. User reports speedtest startup stalls followed by recovery. Synthetic survival gates were insufficient to predict this field performance.

Rebuild incrementally from RC7. Prioritize sub-100 ms field RTT and successful speedtest initiation, with repeatable completion, latency under load and recovery on unstable networks. Keep field results separate from synthetic results. Do not reintroduce a post-RC7 change without comparison to the unchanged RC7 baseline.

Raw field diagnostics and offline Go assets are retained separately in the user's file library/conversations; no account names, raw traces or credentials are included in this public repository archive.
