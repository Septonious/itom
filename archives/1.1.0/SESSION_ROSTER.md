# Authenticated selected-lane handover

The new roster driver fills the gap between accounted Noise key establishment
and the existing runtime. The client no longer receives the DATA issuer identity
or selected mailbox mapping from the cold HTTP fixture. This is a source
integration checkpoint, not a working APK/server release or a performance result.

The production lifecycle owner supplies an authorized account/folder plan and
explicit phone/server connection budgets. Each endpoint supplies already selected
executors. The driver creates no connections, folders, grants, issuer identities
or replacement wallet files. Two selected lanes per active account are required;
a server budget of three remains a budget, not a claim that three sockets were
opened. Ten-account tests select twenty sockets per endpoint while preserving
the authorized phone/server budgets of twenty/thirty.

Before taking ownership, construction validates the full plan, separate DATA and
CONTROL issuers, original server journal account/user/folder membership, selected
mailbox scopes, actual nonzero UIDVALIDITY, writable permanent keywords and server
UIDPLUS support. Executors must be distinct, healthy, idle and unclaimed. The
original Noise handover capability is consumed once, so it cannot subsequently
be used to skip negotiation. Failed constructors claim no executor and spend no
permission. Manual bootstrap handover also cannot mint a second capability.

## Exchange and accounting

Three encrypted, independently accounted CONTROL messages travel through the
existing fast anchor:

1. The server offers a canonical manifest containing original DATA/CONTROL
   store and run identities, the agreed plan digest, both connection budgets,
   selected mailbox incarnations and required capabilities.
2. The client validates the authenticated offer against its actual selected
   lanes, opens the original DATA wallet in place, then sends the manifest hash.
3. The server validates that hash and sends READY with the same hash.

Each endpoint observes all three complete actual mailbox bodies, including its
own publications, before returning its result. Its last publication must also
be known accepted. An APPEND tag, EXISTS notification, socket count or local
`serving` status cannot substitute for peer confirmation. Roster confirmation
still does not mean DATA permission or VPN CONNECTED: a real DATA grant, runtime
activation and the remaining production authorities are separate prerequisites.

The packet has a separate post-Noise roster domain (`IRO2`). Its authenticated
plaintext includes stage and generation; compact manifest entries bind indexes
to the canonical authorized plan. Direction, order, generation, plan, budgets,
issuer identities and selected incarnations are checked inside the pure AEAD
validator before replay advancement. Wallet I/O runs after the crypto receive
lock is released. Successful decryptions continue counting toward the original
key-usage ceiling even when semantic validation fails.

Both publishers reserve the original concrete CONTROL ticket before allocating
a nonce, and durably commit the exact canonical MIME charge before I/O. Server
receive/own observation uses the original journal APIs. The client opens an
existing DATA wallet without erasing or resetting it; a mismatched issuer or
session fails closed. No DATA offer is contained in the manifest. Existing grant,
message, mailbox, response, key, replay, folder and socket bounds are unchanged.
The initial cold-fixture CONTROL allowance remains 32 messages.

Cancellation retains driver ownership, exact pending work and original charges
for a later run on the same object. An uncertain APPEND retains its charged body
and cannot turn into another write or READY. Explicit failed-start Close closes
owned executors and its DATA wallet handle without deleting the wallet file.
Successful Close leaves transferred sockets and the transferred client wallet
handle to the lifecycle owner. Known startup/roster UIDs remain bounded by the
original sixteen-UID handover limit and convey no deletion/refund permission.

`NewRosterRuntime` verifies that the runtime uses exactly the negotiated physical
lanes, agreed ledger identities, confirmed Noise generation, and complete anchor
UID knowledge. It then uses the original runtime's role, service and explicit
budget checks. Diagnostic snapshots expose fixed counters and local monotonic
prepare, APPEND, discovery, wait and elapsed times. They expose no profile key,
mail payload, target address or provider error strings, and are not RTT samples.

## Verification and remaining work

All four cold TLS/HTTP scenarios now negotiate the roster before runtime service,
including fluctuating client/server command service, quiet bulk IDLE, and actual
DATA fence/replacement/final-cleanup/retirement. Ten-account roster negotiation,
cancellation/resume, lost APPEND replies, foreign existing wallets, plan
disagreement, duplicate ownership, malformed manifests and the maximum selected
socket capacity have dedicated checks. Raw evidence and exact source hashes are
in [the roster check manifest](experiments/session-v2/ROSTER_CHECK_RESULTS.json).

A production long-lived profile/rendezvous owner still has to select and inventory
eligible existing folders, reconcile retained journals/wallets and missing wallet
history, apply multi-account authority policy and overlap CONTROL/DATA renewals.
One intermediate repeated quiet-IDLE cold case still hit the original TLS
startup deadline (35/36 group executions passed); that failure and its command
and journal timings are retained, even if a later cohort passes. The UID
visibility fix does not establish the cause or resolution of that TLS failure.
Native client/server entry points remain `V2_NOT_READY`. Sustained independent
process/provider tests and all original 250ms/2000ms/100Mbps and zero-failure gates
remain unmet. No provider performance claim or installable RC follows from these
integrity checks, and removed transport code is not reactivated.
