# Retained FETCH service experiments

All original JSONL files are retained, including failed pilots, incorrect caller
expectations, intermediate functional failures and rejected fast-path grouping.
`FETCH_SERVICE_CHECK_RESULTS.json` in the parent directory indexes outcomes and
hashes. Only its `sources` hashes identify the final source tree; pilot logs
describe intermediate variants and must not be relabeled final-source results.

`before.jsonl` and the three pprof files are from parent `96a1447` with profiling
enabled. The first paired light run overlapped another race build; the later
`group-light-isolated-single/pair` runs stopped other test work. The paired fast
activation was then rejected; final assembly groups bulk reads only.

The competitive/queued cold stress comparison ran before maintenance backoff
was added. To recreate its variants from the final source, combine
`competitive-fetch` with `unbackedoff-maintenance` for the competitive case;
use only `unbackedoff-maintenance` for the queued case. These are counterfactual
variants of the new rewrite, not the removed transport or RC2.

Original overlay JSON files retain their execution workspace paths. Generate
paths for another checkout with `replay-overlays.py`, for example:

```sh
python3 replay-overlays.py competitive-fetch unbackedoff-maintenance --output /tmp/fetch-competitive.json
```

Pass the resulting JSON to `go test -overlay=...` from `source/core`. Negative
overlays intentionally fail their matching regression. They never modify files,
time limits, grants, persistent debt, byte limits or qualification thresholds.

Final source checks are `final-core-ordinary`, `final-core-updated-race`,
`final-native-race`, both vet files, `final-repeated-scenarios` and
`final-preparation-race`. `final-core-race` predates the canceled-restart test's
observable-waiting-condition update; its failure remains retained. The original
healthy-socket, cancellation and undrained-tag assertions were preserved.

`final-light-isolated.jsonl` is the failing 64-exchange application gate.
The stored-mail read component first commits and APPENDs actual encrypted
publications, then times only reading/decrypting/consuming them. Startup and
publication time are excluded. Neither that component nor its short sample
rates establish sustained end-to-end tunnel throughput. `/tmp` is tmpfs;
original bbolt updates/fsync still execute, without physical-disk validation.
Client, server, provider and relay share one two-CPU source host. There is no
independent-host or real-Rambler qualification in these results.
