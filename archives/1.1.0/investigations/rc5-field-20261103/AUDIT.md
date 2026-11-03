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
