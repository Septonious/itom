# Rev4 application and recovery tests

The new suite starts actual client/server supervisors, real TLS/IMAP connections, durable journals/wallets and TCP targets. It checks application bytes and bounded recovery rather than treating lane readiness as success. Retained-state cases reserve 6 MiB/130 server message slots per account with no client history; those historical allocations remain charged.

| Case | Workload and acceptance |
| --- | --- |
| HTTP phase changes | Twelve parallel 256 KiB GETs, twelve verified POSTs, then another GET phase; 240 ms modeled mobile RTT; 4 KiB messages during every phase; each HTTP transfer keeps the same 15 s deadline and messages keep 3 s deadlines. |
| Delayed visibility/reader repair | Eight public-client 1 MiB TCP exchanges, 1.8 s allocated-but-invisible BODY delays, all client DATA readers closed during load; exact bytes on all streams, unchanged logical session and usable 128 KiB message afterward. |
| Retained state/consecutive loads | Twelve 512 KiB exchanges over four accounts, twice; short messages during load and a 128 KiB application exchange after each round. |
| Canceled load/rejoin | Twelve 2 MiB exchanges interrupted by correlated account outages and cancellation; original message TCP survives, followed by a new public OPEN and verified 1 MiB transfer. |
| Parallel startup | Twenty-four simultaneous public OPENs on a modeled 120 ms mobile path; every 32 KiB exchange verifies, followed by a 128 KiB application exchange. |
| Reordered offers and replay | Grant 18 visible before unseen 14 must not fail the lane; duplicate usage remains charged, retired 14 cannot spend again, restart rejects unknown historical IDs, and bounded tombstone pruning preserves the replay floor. |
| Visibility versus global queue | Healthy ACKs and short traffic cannot cause a global reduction solely because another BODY is delayed; loss of useful progress still reduces flight. |
| Short repair discovery | A short unresolved range emits early STATUS but cannot copy DATA without authenticated NACK; bulk timing and charged bytes remain unchanged. Early status is bounded to 4 s. |
| TCP isolation under delayed BODY | One delayed stream retains 192 KiB while a healthy stream advances one charged frame at a time; no second unacknowledged probe, no old-flight refund and no hard-cap bypass. |
| Runnable scheduling and sizing | Active writers/unknown outcomes stay charged and cannot reschedule until an authenticated NACK; ready siblings retain fair turns. Metadata sizing matches physical encoding without copying or mutating application bytes. |
| Authenticated NACK wake | A missing acknowledged range must leave an obsolete admission wait and reach the repair adapter; a duplicate NACK cannot bypass live writer ownership. The focused test failed before the fix. |
| Repaired-path feedback | A completed authenticated repair measures fresh physical delay while retaining original age and all unacknowledged flight; a stalled repair still triggers outage protection. |
| Long blackout | Eight pending 2 MiB exchanges and original message TCP through a 30 s whole-provider outage; no replacement application socket, then a new public request. |
| Authenticated retry | A prior working carrier retries within 3 s throughout the logical survival interval; prolonged/cold failures still reach bounded exponential backoff. |
| Logical answer lifetime | Repeated answers after the original physical carrier is canceled; canceled callers do not receive stale replies. |
| Pre-activation envelope | Old authenticated DATA found in a control mailbox stays in bounded discovery until activation; then it enters proof-based cleanup without application delivery or debt forgiveness. |
| Selected OPEN route | The selected physical carrier receives OPEN while logical ownership stays on another actor; a failed selected carrier falls back. |

All cases are normal Go tests, not opt-in skipped acceptance. The repeated runner selects every required case by exact name, preserves failed logs, requires the same source fingerprint before/after/across runs, and fails missing/skipped cases. Added commit-delay runs retain actual fsync under the itomtest build tag. Race instrumentation is used for correctness separately from normal performance timing.

Removed obsolete tests: the two direct-lane loadedApplication smoke runs; the unused direct-lane/multi-user production runner and its duplicate opt-in entry; the old opening-only development cases; the opt-in consecutive mobile case superseded by mandatory phase/recovery cases; fixed-total-delay assertions that validated the old controller objective rather than application progress. Shared TCP/byte-integrity helpers, protocol/accounting tests, and the exact-byte measurement clock and current six-case throughput qualification remain. Superseded optional 20/50/130/200 Mbps model runners, duplicate startup/fragmented wrappers, their unused HTTP measurement helpers and model20/model130 CLI targets were retired. Owner and non-owner physical-survival tests now run unconditionally. Full builds also execute the current performance case; final qualification repeats it on six fresh endpoints. The stale Rev2 transport matrix tool was removed.

Every expected injected carrier loss is tied to its existing carrier pointer. Replacement-carrier exits are not exempted. A passing suite remains laboratory evidence; actual Android/provider Speedtest and Telegram behavior must be checked independently.

Release performance criteria use the user-authorized 10% tolerance: each direction and sustained tail must reach 90 Mbps (100 Mbps nominal), every loaded RTT must remain strictly below 1,100 ms (1,000 ms nominal), and no probes may time out, be missed or lack overlap with useful traffic. Each SINGLE/MULTI run must pass; failures cannot be averaged away. Probes run every 2 s with a 1,100 ms deadline.
