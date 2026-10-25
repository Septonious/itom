# UID discovery experiments and idle-session coverage

This source continuation follows `b10eefc`, including the user's temporary
[300ms idle/light maximum](TEMPORARY_RTT_POLICY.md). The preferred target remains
250ms. Loaded RTT, sustained 100Mbps, all completion/stall requirements and
original quota/journal/recovery boundaries are unchanged. This is not a release.

## Retained correctness change

At UINT32_MAX the worker now inventories instead of wrapping or stopping a
healthy selected connection. It can still discover late lower UIDs, with the
same pinned UIDVALIDITY and original accounting/proof checks. This independent
numeric-exhaustion fix is retained. It makes no normal-network RTT claim.

## High-water candidate held back

A complete inventory previously replaced the speculative UID tail with the
highest currently live UID, or zero when the mailbox was empty. Cleanup could
therefore make the next allocation pay a failed query for an old UID, another
inventory and a BODY fetch. UID allocation itself does not rewind on deletion.

The candidate preserves the previous observed UID high-water mark while deriving
the current live sequence prefix from actual inventory. It changes only the
speculative next selector. A miss, dependency, sequence gap, ambiguous deletion,
cache pressure or history loss still forces full inventory. Lower late-visible
UIDs are not discarded. UIDVALIDITY remains pinned.

The same bounded metadata and pending-delivery limits apply in the candidate. Hints
cannot authenticate a BODY, advance replay, refund storage or alter persistent
grant/wallet history. Three repeated race groups cover an emptied mailbox, a
shorter retained prefix, late lower UID visibility and numeric exhaustion.
The old-selector overlay fails their corresponding regression checks.

The first matched 64-exchange propagation cohorts average 249.397ms before and
201.091ms after, with 7 versus 2 misses of the temporary 300ms maximum. Another
retained-pacing cohort averages 193.937ms with three misses. All samples and
failures remain in the evidence; these are shared-host synthetic observations,
not a guarantee of field RTT or sustained tunnel bandwidth. The final candidate
warm cohort averages 198.099ms but still has one 300.667ms violation.

The previous discovery behavior completes all nine long-idle requests in three
cohorts, while only one cohort meets every 300ms maximum. The high-water candidate
has both successful quiet cohorts and intermittent header timeouts. The samples
do not prove a sole cause, but warm averages cannot justify activating a change
without convincing stability evidence. The high-water optimization and its
specific performance regressions are archived as an applicable patch, not left
on the default path. Actual long-idle completion/RTT gates remain active in CI.

## Experiments not activated

Reducing idle probe slack from one eighth to one sixteenth of measured empty
command service encountered a header timeout after 32 completed warm samples
and a separate startup failure. Neither run completed the 64-sample gate. The
startup failure precedes active probe service, so it cannot establish causation
by that tuning. The variant is not accepted; original probe pacing is restored.

Reusing immutable parser token mappings improves the isolated metadata parser
from 592 bytes/six allocations to 208 bytes/three allocations per operation.
Its warm application cohort is worse, while one quiet cohort succeeds. That
mixed evidence does not justify activation. The implementation is saved as a
patch, with both parser benchmarks and every application result; production
parsing remains unchanged. Parser CPU/allocation figures are not RTT or Mbps.

## Long-idle failure coverage

A separate tagged scenario measures the first existing-TLS exchange after real
1.13s, 5.07s and 30.19s application silence on the same fluctuating client
10/50/20/40ms and server 20/40/30ms byte-level TLS routes. The original 64-exchange
cohort is unchanged. Only this new scenario uses a 60s total runtime lifetime to
fit the long silence. All original OPEN/TLS/header/client/IMAP deadlines remain
unchanged. CI retains the quiet scenario's JSON even when it fails.

The new coverage exposes intermittent header timeouts, including a request
after 30s silence and another after roughly 1s. A later parser-candidate run
completes all three quiet exchanges, but cannot erase the failures. They are not
yet explained as a silence-duration-specific problem. Failure-only diagnostics
now retain runtime/carrier state, command counters, memory/GC summaries and up
to 512KiB of goroutine stacks after the measured deadline. They do not prime a
request, create quota permission or change timeout handling. Observed stacks
are not an isolated-fsync or sole-cause measurement.

The final default warm cohort has 7/64 violations of 300ms (maximum 742.194ms).
Its quiet cohort completes all three exchanges below 300ms (maximum 265.019ms),
without erasing earlier failures. The final default core race suite passes
234/235 groups; the existing fluctuating-command cold HTTP scenario fails.
That scenario also fails one of three targeted repeats. The independent UID
exhaustion check passes three repeats, and its old-branch counterfactual fails.
Native race checks pass ten groups and both vet checks pass. The earlier
237-group pass belongs to the high-water candidate, not the final activation.

Functional passes cannot qualify the failed RTT/completion gates.
Production initialization/native activation, authority turnover, controller and
receiver-window integration, independent-provider calibration and full sustained
rate qualification remain incomplete. No APK/server release is produced.

Raw cohorts, rejected variants, source hashes and final check outcomes are in
[UID_DISCOVERY_CHECK_RESULTS.json](experiments/session-v2/UID_DISCOVERY_CHECK_RESULTS.json).
