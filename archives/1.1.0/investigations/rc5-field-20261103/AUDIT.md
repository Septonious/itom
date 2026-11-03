# RC5 field failure audit — 2026-11-03

The matched RC5 run initially reaches all 30 roles at 09:01:13 Moscow time.
At 09:03:11 three accounts stop at DATA cursor 32, phase 8 (CLOSE).
Six more reach the same point. The command's ten-second bound then retires
nine complete client accounts, although their direct IMAP timing remains low.
One account crosses a generation successfully. This is direct evidence that
reader-owned CLOSE is not a reliable delivery primitive on this provider.
The server continues control polling and publishes unacknowledged DATA for
those failed readers. The speedtest never establishes a working upload phase.

A second independent failure is reproducible with a real TCP origin that
consumes a request FIN and leaves its response side open. Session cancellation
aborts mailbox streams but did not close the established exit connection.
One bridge copy had already returned EOF, leaving the opposite TCP read and
Session.Close waiting for the remote origin. The next authenticated session
waited behind that teardown. The fix binds established exit sockets to stream
and session cancellation, preserving ordinary half-close response delivery.

## Storage replacement contract under implementation

* DATA receive must never issue CLOSE, DELETE, UID EXPUNGE or wait for cleanup.
* Rotate away from a drained generation into a parking mailbox; notify the
  authenticated producer. Select the producer-announced next physical slot.
* Producers allocate an empty proved-reclaimed slot, or a fresh bounded slot.
  Logical generation is independent of physical slot. No premature reuse.
* A producer may request collection only after reader drainage, packet receipts
  and terminal APPEND tags. The server independently checks inbound ownership;
  outbound ownership can use exact APPENDUID/UIDVALIDITY receipts.
* Optional server housekeeping borrows its existing APPEND owner after draining
  commands; routing excludes that owner while leased. There is no extra login
  per account merely to perform housekeeping.
* Use ordinary EXPUNGE on a fully drained, transport-owned selected slot, with
  exact UID STORE and verified empty STATUS. Never use CLOSE or folder DELETE.
  Never refund unknown outcomes. Whether this provider implements that command
  promptly still needs field confirmation; it cannot gate delivery.
* One housekeeping lease at a time. An unknown outcome opens a shared circuit
  breaker, keeps debt, and stops trying other accounts. A blocked housekeeping
  operation must not cancel working control/DATA readers or poison readiness.
* Parking an old writer interrupts its raw socket, joins pending command owners
  without misclassifying expected cancellation as an account failure, and keeps
  standing control discovery alive for the replacement session.
* Preserve finite retained-byte/slot bounds, user-selected manual server reset
  policy, Noise/TCP reliability, DNS settings, UI theme and signing identity.

Required regressions include CLOSE stalls at the first full generation,
untagged base-EXPUNGE updates, failed/blocked housekeeping, exact ownership and
no premature refunds, complete download then upload, many aborted TLS streams,
user reconnect while a half-closed origin stays silent, and the existing
normal/race/native/weak-link and throughput acceptance cohorts.

## Native admission gap

The client export records 70 worker refusals, 89 failed opens (20 timeouts),
156 established opens, 15 blocked IPv6 attempts, zero DNS lookup failures and
1383 rejected non-DNS UDP packets. The old aggregate refusal counter did not
identify which frontend pool was full, and these counters were absent from the
periodic runtime trace, so they cannot be aligned precisely with the first
CLOSE failure.

The native bridge used the same 128 workers for TCP and DNS associations. UDP
DNS associations retained a worker for 30 idle seconds. A focused regression
reproduces a rejected TCP worker after filling that budget with idle DNS flows.
The fix gives DNS its own bounded 64-worker budget, releases answered idle UDP
DNS associations after one second, and keeps TCP's existing bound. Both UDP and
TCP DNS use the separate budget. Direct resolver endpoints, 250 ms exchange
budget, first-answer race and optional session-long IMAP fallback are unchanged.

The actual native SOCKS/TUN proof now includes 32 concurrent fresh HTTPS posts
alongside 16 incomplete background TLS sockets and 128 DNS-association attempts.
It passes with zero TCP worker refusals. New diagnostics split DNS/TCP refusal
counts and record them with periodic transport snapshots; session admission
refusals are counted separately. This avoids attributing every pre-speedtest
failure to the later storage cleanup failure without evidence.

## Sustained-load follow-up before publication

An intermediate RC6 candidate passed hosted throughput but failed the longer
two-core four-stream upload near its end. Its trace shows several accounts at
their physical byte cap while other accounts receive repeated collection turns.
The single cleanup lease now queues account owners fairly; oldest eligible
records share each writer across directions. In-flight bulk drain time has a
separate bound from cleanup command service, avoiding false cleanup failures on
weak links. After the first fairness fix the same 30-second four-stream model
completed at 233.5 Mbps upload and 269.3 Mbps download with all reported targets
passing. That intermediate evidence is retained, not treated as the final build.

The qualification worker now supports repeated full speedtests in the same VPN
session with fresh TLS each cycle. The matched release gate requires two
30-second upload/download cycles for four streams, plus single/high-stream and
weak-link cohorts. This explicitly tests that the first speedtest cannot consume
all reusable capacity and prevent the next one. Final checks run on frozen source.
