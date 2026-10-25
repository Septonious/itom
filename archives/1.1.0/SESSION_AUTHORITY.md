# Accounted session authority channel

The replacement's cold HTTP path now obtains DATA grants through authenticated,
physically accounted CONTROL mail. Direct fixture acceptance of a DATA offer has
been removed from that path. This remains source integration: production startup
APIs return `V2_NOT_READY`, and no APK/server testing pair is produced.

## Authority and physical storage

A stream-independent `Authority` record carries a bounded original `wire.Control`
frame and an explicit DATA/CONTROL authority class. Its containing message always
uses an original CONTROL wallet/journal permit. DATA cannot enter this channel.
Before replay advancement, pure validation checks peer direction, user, account,
original issuer identity/Run, fresh writer Session and authority class. Current
constructors pin one account and two distinct issuer stores each. A logical
session now registers immutable independent routes for all selected accounts. The actual trusted
issuer mapping is now learned through [authenticated roster negotiation](SESSION_ROSTER.md);
it is not inferred from missing wallet history or an unauthenticated provider hint.

The logical queue is bounded at 64 outstanding records of at most 4096 payload
bytes each, within the existing Session byte limit. Undispatched cumulative
status can coalesce; immutable in-flight records cannot. Publication reserves an
original concrete CONTROL ticket before dequeue/encryption and commits canonical
MIME debt before I/O. Authority records remain separate from progress bundles.
Their turns alternate with ordinary stream controls, so queued metadata cannot
permanently exclude OPEN/credit/receipt service. An uncertain publication retains
its exact charged work; repeating a server policy request cannot mint another
grant or cause an automatic re-APPEND. Trusted exact storage proof resolves only
provider acceptance, never client permission or DATA delivery.

## Durable lifecycle

`ServerAuthority.Issue` is an explicit trusted policy operation with a monotonic
request sequence per ledger. A queued original allocation is idempotent. If the
logical queue is full after allocation, the method returns its original grant
reference; retrying the same request queues that same allocation after real space
becomes available. Ambiguous journal outcomes remain poisoned and cannot become
new issuance attempts.

A verified incoming grant reaches `Wallet.AcceptCurrentOffer` only after Noise
and physical-envelope validation. Publication workers subscribe to actual durable
permission changes. Owned CLIENT body registration can publish a coalesced
original cleanup status. The client acknowledges the registered descriptor prefix
and preserves lifetime `Used`; APPEND acceptance, status delivery and a timeout
cannot reset that counter or release physical storage.

A client fence first stops new reservations on the specified original grant.
Already-admitted tickets can commit/cancel normally, outside the stream actor
lock. The final durable fence includes all of those spends. Cancellation removes
only the volatile admission stop; committed spend and any completed fence remain.
The fence then travels through an independently usable CONTROL allowance. It
cannot create emergency permission. Repeated fence/retirement owner calls do not
duplicate outstanding control work.

Full physical cleanup still requires original BODY ownership and separate
same-incarnation absence proof. The policy owner may refresh status only from the
original journal after such an event. Retirement is an explicit client-owner
operation after durable final cleanup acknowledgement. Server metadata absence
can acknowledge metadata absence only; the original wallet requires its own
already-recorded final cleanup proof before removing a grant. Retired offers do
not revive their writer counters. Progress-only status and retirement-ACK handlers
do not require room for new outgoing authority work.

## Verification and limits

The cold runtime scenario fences a used DATA grant, receives a distinct real
replacement, observes final cleanup, completes retirement over mail, rejects the
retired offer, and continues TLS/HTTP service. The original cold cases retain
1500ms OPEN/TLS/header and five-second HTTP limits, 64 live messages/16MiB, original
32-message initial grants, and fixed socket budgets. Wallet descriptor pruning is
checked against actual durable issuer registration and lifetime spend.

Other checks cover foreign scope/direction/ledger rejection before mutation,
semantic rejection without replay poisoning, unknown publication/exact proof,
queue-full issuance idempotence, sender admission/fence cancellation and stream
actor availability. See `experiments/session-v2/AUTHORITY_CHECK_RESULTS.json` for
raw results, failures retained from the first lifecycle iteration and source hashes.

Per-account wire routing and common service assembly now exist; see
[the integration contract](PER_ACCOUNT_RUNTIME.md). Remaining integration includes
production identity/profile/roster coordination,
existing-profile/journal reconciliation, multi-account authority coordination,
automatic policy for overlapping CONTROL/DATA grants and retirement, old-session
recovery, native activation and the full original performance gates. In particular,
current-session authority routing does not reconcile a reinstalled client's missing
old wallet history. Those charges stay in the original journals. None of these
checks establishes <=250ms light RTT or sustained >=100Mbps on Rambler.
