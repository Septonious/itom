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

RC17 preserves the RC16 complete-generation fast path. After a refusal or a
partial result, surviving UIDs are inventoried and each retry is bounded to
approximately four MiB of actual MIME bytes, permitting one oversized owned
message. The original UID and UIDVALIDITY proof remains mandatory. Deferred
owned Deleted flags are cleared before EXPUNGE; unknown/foreign UIDs refuse
mutation. Historical/consumed-prefix work uses the byte bound as well.

The refused record remains in bounded retry mode until collection completes.
Each successful page returns its writer and shared lease. The complete charge
remains until a fresh empty SELECT; partial work never sends COLLECTED or
refunds capacity. It finishes that direction before alternating and reuses the
existing two-turn preference to make capacity reusable sooner while yielding to
peers. Startup, delivery controller, quotas, profile/crypto identity, DNS and UI
remain RC16's.

Three frozen-RC16 regressions reproduce oversized DATA deletion, partial-retry
rejection and oversized historical mutation. They pass on the candidate. A
separate new test preserves unproved messages, and the existing slow-mutation
case still completes on its original writer without an early refund. Full TLS
qualification adds a refusal model: an independent four-MiB provider budget
rejects large initial attempts, then verifies bounded accepted retries, completed
transfers and audited retirement in both directions. Expected quarantines must
recover. This fault case retains latency/functional gates; its throughput goal
misses are reported. The existing healthy-provider throughput gates are unchanged.

At the user's request, folder names switch directly to the compact `itom-`
layout (31-character control, 55-character slotted DATA). Both full 128-bit
tokens are retained; fixed-width parsing handles hyphens inside base64url.
The accounts have already been cleaned manually. No legacy discovery or
migration is implemented. The disjoint layout has its own durable capacity
store and retains normal automatic cleanup and conservative restart accounting.
Both endpoints must be updated together. Profile/crypto identities are unchanged.

The first four-MiB prototype reproduced a capacity stall in complete TLS
transfers: too many metadata round trips kept every generation charged. Merely
adding continuation preference restored completion but second-cycle throughput
remained 84.5/51.0 Mbps. It is rejected. Eight-MiB pages reduce metadata cost;
partial DATA work finishes its direction before alternating, and the existing
two-turn recovery preference yields regularly to peers. Every page still returns
the writer and shared lease, and only the final empty audit refunds capacity.

The eight-MiB continuation prototype completed but second-cycle throughput
remained 85.4/83.4 Mbps, also below the unchanged full-phase gate. It is not
eligible for publication. The next candidate uses sixteen-MiB pages, retaining
byte bounds, resumable proofs, final audits and the existing fairness limits.

The sixteen-MiB always-paged prototype also completed but second download fell
to 68.0 Mbps. It is rejected. These experiments establish that adding extra
metadata turns to every healthy generation regresses repeated transfers. The
final approach preserves RC16's fast initial collection and enters bounded mode
only after an actual refusal/partial outcome. This also keeps the fix small.
