# Per-account roster and runtime assembly

The replacement now negotiates and uses each account's original CONTROL/DATA
issuer IDs and current runs. A ten-account TLS/HTTP scenario uses twenty distinct
journal files and the corresponding persistent wallet files rather than shared
fixture stores. Old-session DATA wallet spend is retained and fenced in place.
The independently selected phone/server sockets remain within twenty/thirty
budgets. No installed journal is replaced or combined with another account.

## Authenticated binding

`NewServerAccountRoster` receives already-open original journal handles per
account. Each CONTROL journal must authorize the existing fast folder; DATA must
authorize both fast and bulk. CONTROL does not need to gain an extra bulk-shard
folder. This allows the existing two-folder CONTROL and ten-folder DATA layouts
without extending either or losing the unused legacy inventory.

`NewClientAccountRoster` receives fixed local wallet paths from the persistent
identity owner. Mail can select neither a filesystem path nor a replacement
identity. It authenticates the complete manifest, including account order,
UIDVALIDITY, ledger separation and consistent runs for repeated issuers, before
opening any additional CONTROL/DATA wallet. All required wallet opens must
succeed before ACCEPT. A failed open keeps the original file/history, sends no
acceptance, and leaves owned handles for explicit failed-start close. The already
open anchor CONTROL wallet remains borrowed. Successful results transfer their
new handles through `Wallets(account)` and `CloseWallets`; close them only after
runtime service has stopped. Original server journals always remain borrowed.

The manifest's unqualified wire format is now version 2. No previous roster wire
was deployed as a production release. Account entries carry both original store
IDs and runs. Up to four canonical OFFER chunks cover the existing 2048 selected
account / 4096 socket capacity. Each keeps the existing 32KiB roster-body and
original secure-packet limits; short, duplicate-offset or mixed-hash parts fail before replay advancement.
Authenticated parts may arrive in any order within the same fixed bound; gaps
cannot expose a complete manifest or open wallets. Complete manifest metadata is bounded at 122980 bytes
(100-byte header plus 60 bytes per selected account), rather than the earlier
shared-store-only 32KiB total. This is an explicit bounded startup allocation,
not a larger DATA window or queue. The original sixteen retained startup UIDs
still cover eight initial bodies, four offer parts, ACCEPT and READY. Existing
quota, folder, mailbox, response, grant, fsync, nonce and replay ceilings remain.

`NewServerRoster` / `NewClientRoster` remain explicit shared-issuer adapters for
scopes that really share the original files. The client adapter rejects a
per-account manifest instead of flattening its identities into one wallet.
`ProfileServicePlan.RosterSelection` preserves all selected accounts for the new
constructors; `SharedRosterPlan` still refuses separate issuers deliberately.

## One session, separate authority

The logical session now registers one immutable authority route per account,
each still pinning two distinct original ledgers, authenticated user and fresh
writer session. Registration publishes a new immutable routing view before
runtime starts. Receive validation captures that view without a per-body account
map copy. Account, issuer, run, direction and ledger checks still precede Noise
replay advancement. Durable handlers run outside the session actor lock.

The shared outgoing authority queue stays at its original 64-record bound and
within original session memory. An account's metadata may travel on another
account's legitimately charged CONTROL carrier; its inner original quota key
still selects exactly one authority. A regression rejects a wrong account issuer
mapping, then accepts the same valid encrypted body with the correct mapping
without poisoning shared replay or changing another account's wallet.

`NewRosterService` is the common client/server runtime assembler. It consumes a
completed roster once, constructs scoped publishers/receivers, original proof
reclamation/client receipts, workers, per-account authority and the TCP exit or
client dialer. Unknown publication and cancellation semantics are unchanged.
It issues no grant and cannot advertise VPN readiness merely from local service.
Failed stateful assembly stops its own session/carriers while retaining durable
files and borrowed authorities. Wrong keys or duplicate assembly are rejected.

The new application test begins traffic only after actual current-session DATA
and CONTROL offers arrive for every account. Server issuance is explicit trusted
policy, with the original 32-message initial allowances; this test does not
implement automatic renewal. Real TCP, TLS and four HTTP echoes cross the
assembled runtime under unchanged OPEN/TLS/header/HTTP deadlines. Twenty original
issuer files and retained old spend are audited. These are integrity tests, not
calibrated Rambler RTT/bandwidth qualification.

## Wake after proven capacity release

A second reproduced defect left server writers parked after actual reclamation:
publication admission woke on authority messages, but not on an independently
proven deletion freeing local journal capacity. Successful original
`DeletedMany` now notifies the matching account/ledger authority watcher. It
emits no grant or refund and does not reset wallet spend. Neither an EXPUNGE tag
nor inventory absence alone invokes this wakeup.

The regression parks a real server publisher with its exact physical budget
held, delivers and receipts an original body, holds EXPUNGE completion, and
verifies that no permission appears before separate absence proof. Actual proof
then resumes the writer without any unrelated message or manual notify. An
unrelated original remote reservation remains fully held. Removing only the
notification reproduces the parked writer after cleanup; the fixed case passes
repeatedly. The initial test caller's diagnostic-lock stall is retained too.

## TLS failure, preparation coordination and diagnostics

The previous quiet-IDLE TLS startup failure reproduced in 1 of 10 repeated runs.
A repeated ten-account cohort also missed a response-header deadline. Failures
retain their original limits; the source host has a shared two-CPU budget.
Aggregate journal maxima alone cannot establish a request's blocking cause.

Review found a concrete source of excess metadata work: independent server
writers could all see the same small ready flight, each reserve an original
local grant, then cancel their unused grants when another writer consumed the
flight. Fast preparation now claims bounded logical preparation intent before
those durable reservations. It dequeues/encrypts nothing and supplies no quota;
the original concrete ticket is still required first. Claims are released on
all outcomes and wake parked workers. Single OPEN/RESET/authority messages remain
independent, ordinary progress uses its existing container bounds, and DATA uses
actual ready demand. Bulk retains independent preparation that fits each concrete
account allowance. No batching wait is introduced.

A deterministic tagged regression holds real durable updates for 100ms before
real fsync. Without this coordination, ten writers do twenty journal updates for
one TLS flight; the candidate does two and retains exactly one charged body.
The baseline uses a Go source overlay removing only this guard. Other cases
prepare independent bulk bodies with overlapping journal updates, including a
small burst split across accounts whose individual quota only fits part of it. Neither
result is a useful-byte rate or a complete RTT/performance qualification.

`journal.CommitTimeline` observes an in-progress update without taking the
journal/database lock, and retains a detached bounded set of eight slowest
completed updates with process-local monotonic start/end times. It records no
keys, payload, profile strings or operation arguments. The cold test records its
request interval against that same clock on failure. Updates still synchronously
commit with fsync and ambiguous acknowledgements still poison permission. These
measurements describe complete journal updates, not isolated fsync or path RTT.
The deterministic preparation tests are also part of source CI; that workflow
still cannot package or authorize an APK/server release.

The post-fast-coordination repeated application cohort passes 19/20 cases,
retaining one ten-account response-header timeout. A subsequently rejected bulk-coordination
cohort passes 7/10; the differing small samples establish no reliability or
throughput improvement. A deterministic partial-allowance regression then shows
that bulk guard permits only one account preparation for a burst requiring
several original accounts. That could sacrifice rate near quota exhaustion.
The bulk guard is removed; the candidate must preserve overlapping preparation
in both large-backlog and partial-allowance cases. A new ten-account fluctuating-service cohort (client
10/50/20/40ms, server 20/40/30ms per command, original real TLS/IMAP and fsync)
passes 2/3, retaining another header timeout. These are command-service models,
not calibrated independent network paths. Per-request flow/timeline samples
retain the stalled work; the largest recorded journal update was before that
failed request, so it cannot explain the request's pause. Preparation amplification is fixed in
its deterministic regression; the zero-failure application gate is still not
met. No successful cohort erases this retained failure.

The retained candidate passes 215/215 full core race groups, 10/10 native groups,
both module vet checks and six tagged preparation group executions (plus six
bulk subcases). Six final ordinary-build application integrity groups pass, but
the warm diagnostic target still fails: one of twelve loopback samples reaches
1119.979ms, and four of twelve fluctuating-service samples exceed 250ms (maximum
305.437ms). Their causes are not established. These samples remain disqualifying
alongside the earlier deadline failures; a green source suite is not a green
performance gate.

Raw results, retained failures and source hashes are in
[the account integration manifest](experiments/session-v2/ACCOUNT_RUNTIME_CHECK_RESULTS.json).

## Quiet receive wakeups

A receiver with no deferred incoming delivery no longer leaves IDLE or shortens
its probe wait merely because another account has outgoing data or completes a
publication. A blocked incoming delivery still samples the session wake before
polling, so actual freed application space is not lost. Mailbox notifications,
bounded missing-notification audits, writer preemption and context cancellation
retain their original roles. The regression holds a quiet real selected IDLE
socket, queues unrelated outgoing bytes without publishing them, and verifies
that no DONE is sent. Removing only this wake filtering reproduces the needless
DONE. The first version incorrectly counted the fixture's legitimate initial
EXISTS transition as the defect; the corrected test first waits for quiet IDLE.
Both the initial test-caller error and corrected before/after checks are kept.

Enabling fast-lane IDLE was tried through source overlays. It reduced empty fast
queries, but the first five-run cohort still timed out once; a later combined
trial had a long cohort outlier. It is not adopted. The fast plane continues its
existing independently bounded probing; bulk retains existing quiet IDLE.
No synthetic command reduction is reported as a Rambler RTT improvement.

Warm HTTP diagnostic samples now reuse an established connection, separating
light-load service from cold OPEN/TLS establishment. The first ordinary-build
cohort using the rejected bulk experiment passes byte integrity but only 9/12
fluctuating-service light samples meet 250ms (maximum 303.016ms). All samples,
including failures of the latency target, are retained independently of source
integrity test success. Neither a passing integrity test nor removal of race
instrumentation can waive that target. CPU profiles of both builds are retained;
the race profile is dominated by instrumentation, which is diagnostic context,
not a reason to discard its observed header failures.

## Still required before a working APK/server candidate

A production long-lived profile/rendezvous owner must connect actual paths,
verified provider capabilities, complete original inventory/reconciliation,
current-session startup and the assembled service to the native entry points.
Missing old wallet history must remain charged; retained debt cannot be treated
as empty capacity. Automatic overlapping CONTROL/DATA policy, old-session
recovery, native TUN activation and the measured-service controller still need
full integration and sustained tests. Preparation coordination addresses a reproduced metadata amplification defect;
all deadline failures remain in the evidence and still require sustained
qualification. A passing repetition does not prove universal failure resolution.

Production APIs remain `V2_NOT_READY`; no working APK/server pair is packaged.
The original <=250ms idle/light, <=2000ms loaded, sustained >=100Mbps each way,
full ramp/tail/rolling/stall and zero TLS/reconnect failure gates remain unchanged
and unqualified. Android package/signing identity, stable 1.0.0/RC2 and DNS race /
session-latched IMAP fallback behavior are untouched.
