# RC16 real 5G results and final collection audit

The screenshots and trace rates are consistent with ITOM at 102/28.9 Mbps
(download/upload), versus VLESS at 156/42.4 Mbps. Idle RTT is about 250 versus
62 ms. This is useful real-provider evidence for RC16. Much of the throughput
gap is consistent with base64/MIME expansion (roughly 1.37x before headers);
sequential 5G tests and distinct provider routes do not establish an exact ceiling.
Raw exports remain private. Older RC13–RC15 rotations are excluded below.

All ten accounts/thirty roles remain ready. There are no carrier failures.
Upload delivers approximately 24–37 Mbps while the interactive drain guard is
often active. The server starts DATA collection near the download/upload
transition, then times out in EXPUNGE on seven accounts. Client recovery begins
with about 268 MB of deferred old debt; final retained data reaches 353 MB.
These are maintenance quarantines, not disconnected carriers, but continued
failure would consume physical headroom on later runs.

A full DATA generation has 128 containers and can contain tens of MiB. RC16
limits historical/control collection by UID count but not MIME bytes, and DATA
collection marks the entire generation. Its retry also accepts only the original
128-message count or an empty folder: a provider that partially expunged before
an unknown outcome cannot resume safely through that branch. The logs establish
EXPUNGE timeouts; exact provider service cost remains an interpretation.

RC17 bounds each collection turn to approximately four MiB of inventoried MIME
bytes, with at least one owned message permitted for oversized bodies.
The UID and UIDVALIDITY ownership fences remain mandatory. Previously flagged
owned messages deferred to a later page have their Deleted flag removed before
ordinary EXPUNGE, preventing an old timed-out full deletion from expanding the
new turn. Fresh inventory reconciles surviving UIDs after a partial unknown
outcome. Unknown/foreign UIDs are refused before any mutation.

Each successful DATA page returns the writer and shared lease. The complete
physical charge and original ownership proof remain until the final empty
SELECT audit; partial pages never send COLLECTED or return capacity. Historical
and consumed-prefix collection use the same byte bound and retain their exact
UID audit. The startup path, delivery controller, interactive guard, profile,
connection limits, physical quotas, DNS policy and UI theme remain RC16's.

Three frozen-RC16 regressions reproduce oversized DATA deletion, partial-retry
rejection and oversized historical mutation. They pass on the candidate. A
separate new test preserves unproved messages, and the existing slow-mutation
case still completes on its original writer without an early refund. Full TLS
qualification additionally enforces an independent four-MiB provider mutation
budget, complete repeated transfers, audited retirement in both directions,
no quarantines and the unchanged latency/throughput gates.

At the user's request, folder names switch directly to the compact `itom-`
layout (31-character control, 55-character slotted DATA). Both full 128-bit
tokens are retained; fixed-width parsing handles hyphens inside base64url.
The accounts have already been cleaned manually. No legacy discovery or
migration is implemented. The disjoint layout has its own durable capacity
store and retains normal automatic cleanup and conservative restart accounting.
Both endpoints must be updated together. Profile/crypto identities are unchanged.
