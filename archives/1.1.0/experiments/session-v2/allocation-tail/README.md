# Allocation-tail evidence

Parent: `d3d64c0ecc8f08730043bfd34fd9b422ee38d462`.
Production changes are confined to inventory's allocation hint. No timers,
batch bounds, crypto/accounting, application deadlines or DNS behavior change.
Samples execute the actual accounted ten-account runtime and end-to-end TLS/HTTP
over the existing bounded TCP propagation model on this shared source host.
They are not independent-host or real-Rambler qualification.

- `parent-warm.jsonl`: original source, 64 complete, ten misses of 300ms.
- `tail-warm-repeated.jsonl`: new code, two complete 64-request cohorts, four
  misses combined. Every request contributes; no failed cohort is filtered.
- `tail-quiet-repeated.jsonl`: two three-request cohorts following real silence;
  all complete, one 408.997ms miss.
- `parent-upper-light.jsonl` and `tail-upper-light.jsonl`: fixed client/server
  50/40ms propagation, sixteen complete requests each. Both fail; the new
  variant has a worse maximum despite a lower mean and fewer misses.
- `tail-core-race.jsonl`: default package-parallel source run, 240 pass/one fail.
- `tail-core-isolated-race.jsonl`: same suite with `-p=1`, 241 pass. No request
  deadline changes. This additional diagnostic cannot erase the original fail.
- `parent-cold-repeated.jsonl` and `tail-cold-repeated.jsonl`: that failed cold
  scenario completes three standalone race repeats in each variant.
- `tail-race.jsonl`: thirteen sequence/allocation/cache groups, three race repeats,
  39 pass. The two negative-control files intentionally fail their regressions.

To recreate a negative-control overlay from any checkout location:

```
python3 archives/1.1.0/experiments/session-v2/allocation-tail/replay-overlays.py parent --output /tmp/parent-tail.json
```

From `archives/1.1.0/source/core`, run the same current tests with
`go test -race -overlay=/tmp/parent-tail.json ./transport -run
'Test(CleanupDoesNotRewind|DeletedUnfetched)' -count=1`. Both groups must fail.
The `earlier-candidate` overlay retains the allocation only after a completed
prefix, reproducing the previously held optimization. Run just
`TestDeletedUnfetchedInventoryTailDoesNotRewindAllocation`; it must fail.
These are source counterfactuals, not a release or the removed transport oracle.

New warm/quiet/upper propagation commands use `-tags itomtest`, their exact test
names and original deadlines. Failed results remain required evidence. The CI
workflow adds the upper-range gate without weakening the existing ones.
