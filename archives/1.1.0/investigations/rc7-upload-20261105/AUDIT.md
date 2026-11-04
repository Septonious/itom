# RC7 field investigation — burst admission and upload flight

The November 4 client/server captures are treated as measurements, not as
instructions. User reports reliable survival and manual reconnects, failed early
MULTI runs, acceptable download and RTT, and poor upload on both successful runs.
Screenshots show 38.6/4.17 Mbps with 198 ms baseline and 77.2/7.59 Mbps with
173 ms baseline. Synthetic rates are not substituted for these field results.

## Findings supported by the captures

The client reaches 64 active streams during early MULTI attempts. Its session
reports 78 local OPEN capacity refusals; native diagnostics report 366 attempts,
213 established, 151 failures and no open timeouts at export (two still opening).
The 78 local refusals are a demonstrated cause of connection setup failures.
Remaining failures cannot all be attributed to that cause: ordinary target dial
failures or application cancellation are not distinguished by RC7 telemetry.
Ten DNS worker refusals also occur, with zero DNS exchange failures and no IMAP
fallback. RC8 leaves the authorized DNS routing policy unchanged.

During the successful first upload, sampled outbound flight stays near 492,915
bytes against the initial 524,288-byte limit. Delivery RTT is 396–462 ms despite
generally 9–43 ms direct client IMAP samples. A later upload reaches 594 ms RTT,
has unsent bytes and retransmissions, and spends most samples at that same
512 KiB limit. Native received application bytes and mailbox publication both
advance slowly. This is evidence of a sender flight bottleneck, not an account
login problem. The independent control ping does not measure bulk publication.

All 30 client roles remain available in this capture. The server export includes
older rotated RC5 trace rows; these are excluded when evaluating RC7 behavior.
RC7 cleanup reports bounded maintenance timeouts while all accounts continue
serving. That provider behavior remains a reason to retain independent readers,
conservative charges, bounded retry and the prohibition on automatic DELETE.

## Changes

Keep 64 active streams and the existing per-stream/shared receive-memory bounds.
An excess local OPEN waits within its caller deadline instead of immediately
failing. Closing a stream wakes admission waiters. When remote RESET and fresh
OPEN cross, a saturated server withholds the OPEN receipt until retry can admit
it, instead of resetting a new connection. Cancellation before admission creates
neither a stream nor an OPEN packet.

Bulk flight covers measured DATA-receipt propagation plus 350 ms finite queue
headroom. Actual dispatch pressure permits a maximum 50% probe once per bulk
observation round. Extra DATA delay stops probing; light traffic cannot train
bulk bandwidth. The initial 512 KiB limit and absolute 32 MiB flight cap remain.
New diagnostic counters distinguish admission waits from refusals and expose
bulk receipt minimum RTT, delivered-byte rate and flight saturation.

No mailbox format, handshake, account socket, cleanup, persistent signing,
application credit, DNS policy or UI changes are needed for these corrections.

## Reproductions and acceptance

The RC7 controller with 500 ms DATA receipts remains at 8.4 Mbps. The candidate
must exceed 100 Mbps in the same demand/receipt-round fixture. RC7 rejects a full
stream-set OPEN immediately; the candidate waits and honors cancellation. A real
TLS IMAP/Noise/TCP replay begins with 64 established background sockets and adds
16 queued opens, closes 16 prior streams, and verifies all 16 new echo responses
without any warm-up SINGLE transfer or capacity refusal.

The network qualification fixture now separately models DATA APPEND service
time. Both endpoints use the same 200 ms provider service delay while their
control path retains the independently varying propagation model. A paired
frozen-RC7/candidate replay must improve upload by more than 50%, preserve all
accounts and the session, keep idle/post RTT within 300 ms and loaded RTT within
2000 ms. Ordinary fast-path and 7 Mbps constrained-link checks remain separate.
All measurements and strict target misses belong in the release evidence.

Final hosted upload comparison: 5.74 → 22.81 Mbps. Final ordinary one/four/32-stream gates, weak-link cancellation, normal/race/native/SDK/vet and Android compile/tests/lint passed. The six actual-binary startup cycles and manual-stop capacity audits passed. Source: `66625b9c3dd40ff7ed60e338bfcae72ee4e9f8e6`.
