# UID allocation prediction across cleanup

An empty or shorter live inventory previously rewound the next-UID prediction.
The next small message could therefore require an empty BODY query, inventory,
and another BODY query. Allocation does not rewind within pinned UIDVALIDITY.
The receiver now retains the highest observed UID as a hint, including when its
BODY disappears between inventory and FETCH. Live sequence position still comes
from the current inventory; missing predictions, late lower UIDs, sequence gaps,
unknown deletions, history overflow and pending deliveries still force inventory.

This is not an application delivery cursor. Actual BODY authentication, durable
registration, replay, client-MAC receipt, exact EXPUNGE and independent absence
proof remain unchanged. Neither inventory nor the allocation hint releases debt,
advances replay or creates permission. No socket, response, literal, memory,
batch-size or timeout bound grew. The temporary maximum remains 300ms, with
250ms preferred, loaded RTT 2000ms and sustained 100Mbps each direction required.

Four new scenario groups cover empty/shorter inventories, an unfetched tail
deleted before BODY arrival, late lower UIDs, and allocation holes. Together with
existing sequence/cache/exhaustion checks, 39 repeated race groups pass. Parent
code fails the two cleanup/vanished-tail regressions. The earlier held-back
high-water candidate also fails the vanished-tail case; this change records the
observed allocation even when that inventory cannot establish a complete prefix.

The parent fluctuating-path cohort completes 64 exchanges averaging 232.204ms,
with ten misses of 300ms. Two new complete cohorts average 199.527ms and
186.350ms, with three and one misses. Their aggregate average is about 17% lower.
Every miss is retained. Both warm cohorts still fail qualification. All six
exchanges after actual 1.13/5.07/30.19-second silence complete, but one takes
408.997ms, so quiet qualification also remains failed overall.

A new gate holds client/server RTT at the upper requested values, 50/40ms,
throughout each exchange. All 16 requests complete in both variants. The parent
has 16 misses and a 371.182ms mean; the new variant has eight misses and a
346.638ms mean. The new maximum is worse: 798.908ms versus 473.172ms. Its trace
also records delayed diagnostic ticks, but this does not prove a sole cause or
permit subtracting scheduling time. Both upper-range gates FAIL. CI retains
this additional case and its failures instead of testing only faster phases.

The ordinary package-parallel core race run passes 240/241 groups; the cold
window-crossing HTTP scenario times out. It then passes three standalone repeats
in each variant, and all 241 groups pass with source packages scheduled one at a
time (`-p=1`). That diagnostic run does not erase the original failed run or
prove a sole cause. CI's original core scheduling and all request deadlines are
unchanged. Typical RTT improves in these source-host samples; maximum RTT and
stability are not established. No field improvement or sustained-rate claim is
made, and no APK/server release is produced.

See the [results and hashes](experiments/session-v2/ALLOCATION_TAIL_CHECK_RESULTS.json)
and [raw evidence/replay instructions](experiments/session-v2/allocation-tail/README.md).
