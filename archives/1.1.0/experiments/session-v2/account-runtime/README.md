# Account runtime source evidence

These files retain successful checks, intermediate build/caller errors, observed
application deadline failures and rejected experiments. They are not a release
qualification dataset. The parent manifest lists hashes, commands, source bounds,
overlay mappings and the observed warm latency misses.

The final full core race cohort passes 215 top-level groups and native passes 10;
both module vet checks pass. The final ordinary-build account cohort passes six
byte-integrity groups. Its warm samples nevertheless miss the original 250ms
limit five times: one of twelve loopback samples (1119.979ms maximum), and four of
twelve fluctuating-service samples (305.437ms maximum). Source success cannot
turn either profile into a latency qualification pass.

`account-final-core-all.jsonl`, `account-final-native-all.jsonl`,
`preparation-final-race.jsonl` and `account-final-normal-light.jsonl` describe the
retained source candidate. Earlier cohorts can describe a subsequently rejected
bulk guard or fast-IDLE experiment. The CPU-profile ordinary runs are a different
cohort from the race-profile runs, not a direct rate comparison. Neither tests
sustained 100Mbps or runs a production native client/server.

The profiles contain source-test function samples; compiled profiling test
executables are deliberately not release artifacts and are not included.

To reconstruct an overlay from the parent manifest, choose its `overlays` entry,
resolve each replacement key against this repository root and each mapped file
against `archives/1.1.0/experiments/session-v2`, then write a Go overlay object of
this form using those absolute paths:

```json
{"Replace":{"/checkout/archives/1.1.0/source/core/transport/server_publisher.go":"/checkout/archives/1.1.0/experiments/session-v2/account-runtime/overlays/without-fast-preparation/server_publisher.go"}}
```

Pass the resulting file through `go test -overlay /absolute/overlay.json` from
the core module. The no-preparation baseline should fail
`TestTenServerWritersDoNotFsyncUnusedGrantsForOneTLSFlight` with twenty durable
updates. The no-reclamation-wake baseline should fail the proven-release wake
regression; the global-wake baseline should interrupt quiet IDLE unnecessarily.
The rejected bulk-guard overlay should fail the `partial_account_allowance`
subcase of the tagged bulk concurrency regression. Rejected experiments and
expected-negative baselines must never count as passing qualification runs.

Raw historical overlays were captured as work progressed. A historical baseline
may use the source APIs of its captured cohort; use the documented current
replacement set and matching regression when reproducing the final comparisons.
