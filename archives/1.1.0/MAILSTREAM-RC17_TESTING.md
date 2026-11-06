# ITOM 1.1.0 MAILSTREAM-RC17 — final collection optimizations

RC16's real 5G comparison reached about 102/28.9 Mbps download/upload against
VLESS at 156/42.4 Mbps. RC17 preserves its transport and startup behavior while
addressing oversized and non-resumable background collection found in the logs.

Healthy complete DATA generations keep RC16's amortized collection path. After
a refusal or partial deletion, the account remembers bounded collection for
its lifetime. Retries inventory surviving owned UIDs and limit
each turn to four MiB. Previously flagged owned messages beyond the page are
deferred safely. Writers and the mutation lease return between pages; the entire
storage charge remains until an empty-folder audit completes. Foreign UIDs,
changed validity and unknown outcomes never authorize a refund. Historical and
consumed-prefix collection use the same byte limit and ownership fences.

Housekeeping diagnostics capture one ownership instant rather than mixing flags
from different times; the existing two-writer admission limit stays unchanged.

Matched normal/race, SDK, pinned-fork, Android, restart, weak-link, mobile,
corporate, short-transfer and repeated-cleanup checks remain mandatory. An
additional fault model refuses deletions over four MiB and requires automatic
bounded retries, capacity reuse and recovery from the expected quarantines.
The existing capable-path throughput and RTT gates are unchanged. No physical-provider speedup is claimed
before another field measurement. RC10 remains the first field-ready baseline;
RC16 remains an immutable real-5G reference.

Folder names now use only the compact `itom-` namespace: control names are 31
characters and slotted DATA names are 55. Full 128-bit namespace/session tokens
are retained. There is no legacy-folder migration; the accounts were already
cleaned manually. The new layout uses its own capacity store, which persists
normally across reconnects. Automatic cleanup applies to this layout.

Update both endpoints together. Install the signed APK in place; extract the server .bin
into /tmp and use manager option 18. No manual mailbox cleanup or profile reset
is required. Application ID, persistent signer, theme and DNS policy are unchanged.
Version code: 10142. Final results and verified downloads accompany the package.
