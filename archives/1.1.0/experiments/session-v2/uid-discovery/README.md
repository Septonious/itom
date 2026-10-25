# Cleanup and idle experiments

The parent-directory manifest indexes every raw cohort and source hash. Current
policy is the user's temporary 300ms maximum, with 250ms retained in diagnostics.
Network phase/audit/DNS timers of 250ms are separate and unchanged.

The final default retains only numeric UID-exhaustion safety, added quiet-case
coverage and failure diagnostics. High-water prediction, faster probe pacing
and parser caching are NOT active. Candidate patches are archived for review,
not treated as accepted implementations or passing qualification.

File stages:

* `policy-tests` is the original independent policy check from `b10eefc`.
* `rewind-light-300` and `highwater-light-300` compare complete warm cohorts.
* `sixteenth-light-300` contains two incomplete trials: one header timeout and
  one startup failure. Its 32 completed exchanges cannot become a 64-sample mean.
* `eighth-light-300` restores original pacing while keeping the candidate UID
  change. `highwater-regressions`/`rewind-negative` test that candidate's behavior.
* `quiet-light-300` and `quiet-stall-diagnostic` are candidate UID trials, before
  and after bounded failure diagnostics respectively. Both fail completion.
* `parser-uncached/cached.txt` measure parser allocation/CPU only. Files named
  `final-warm-light-300` and `final-quiet-light-300` still have the parser-cache
  candidate active. Their mixed application results did not justify activation.
* `retained-light-gates` and `retained-core-race` predate the later decision to
  hold back high-water prediction. The 237-group pass is NOT final-default proof.
* `rewind-quiet-light-300`, `rewind-quiet-repeated` and
  `highwater-quiet-repeated` retain all comparison trials, including threshold
  misses in otherwise complete cohorts.
* `final-default-light-gates` and `final-default-core-race` describe the actual
  final activation. The warm gate and one cold functional scenario still fail.
  `final-max-and-cold-repeated` characterizes that cold failure without erasing it.
  `final-max-negative` must fail on the old UID-exhaustion branch.

Generate portable overlays, for example:

```sh
python3 replay-overlays.py highwater highwater-tests --output /tmp/uid-candidate.json
```

Run `go test -overlay=...` from `source/core` for the desired existing scenario.
The additional candidate tests are virtual overlay files, not skipped tests in
the default module. Original JSON overlays preserve their execution paths;
the generator rebases them for another checkout. Parser caching is provided as
an applicable patch for a separate checkout. Do not modify deadlines or treat
any candidate run as an independent-provider or sustained-throughput result.

The old maximum-UID test combined candidate high-water behavior with exhaustion.
The final independent exhaustion check keeps the maximum UID live, adds a late
lower UID, and requires actual inventory without wrapping or closing the carrier.
No correctness/deletion/financial requirement is removed.

Quiet cases wait for actual application silence of 1.13s, 5.07s and 30.19s. Only
that new scenario has a 60s total runtime lifetime; original per-request and
per-command deadlines remain. The original 64-exchange scenario still has its
30s runtime lifetime. Missing samples and HTTP failures stay failures.

All endpoints and TLS byte relays share a host with a two-CPU cgroup quota;
failure snapshots observe automatic GOMAXPROCS=3. No CPU setting was changed to
obtain a result. Original durable updates/fsync run on tmpfs; neither coincident
long commits nor runnable stack frames prove the cause of a timeout. Provider
commands, role-specific accounting and actual encrypted application exchanges
execute, but this is not real Rambler, Android or independent-host qualification.
