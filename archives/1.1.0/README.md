# ITOM 1.1.0 DEV4 testing candidate

The current source/ contains the conservative, delivery-clocked DEV4 controller selected for the next field test. Stable 1.0.0/RC2 is unchanged. Android retains org.itom.client and uses versionCode 10104.

[Testing notes, measurements and limitations](DEV4_TESTING.md) document 20/20 original performance rounds versus RC2 16/20, passing full correctness checks, improved retained light-load means and upload supply, and the remaining 8–13% retained simultaneous-duplex download loss and mixed sparse tails. No thresholds were relaxed. Real Rambler/Android qualification is pending.

Build using [the DEV4 workflow](../../.github/workflows/itom-v110-dev4.yml). The [selected immutable comparison](experiments/refill-baseline-cadence/results.json), [reproduction recipe](experiments/refill-baseline-cadence/stage.py), [migration guidance](MIGRATION.md), [DEV2 checkpoint](checkpoints/DEV2/README.md), historical DEV3_BUILD.json and earlier experiment reports remain available. DEV3 source is frozen in checkpoints/DEV3/source.
