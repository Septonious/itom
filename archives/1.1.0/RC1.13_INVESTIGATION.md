# RC1.13 comparison with the last working transport

DEV9 is the last pre-rewrite release in this conversation whose user reports
include successful external Speedtests. Those results were poor and intermittent;
they establish working application traffic, not an acceptable transport. RC2,
preserved as final 1.0.0, remains the user's last usable baseline. A later v2
report saying "connected" explicitly said that external apps did not work; it
must not be treated as a working successor.

The DEV9 runtime source is 96a592f4a9278d069a248e38ddb645a914bde6aa.
Its lane owners schedule bounded cleanup independently of admission of the other
accounts. Its idle fallback janitor is separately scheduled. The subsequent
session transport adds a full authenticated historical inventory, then executes
optional physical reclamation before opening each account's service sockets.
In both immutable RC2/final 1.0.0 and DEV9, `core/tunnel/resources.go` releases
the login permit immediately after `dial` returns; it keeps the separate socket
reservation until close. The rewrite changed that distinction accidentally:
its worker permit spans inventory and optional cleanup. The repair restores the
original meaning of login concurrency without reverting the wire protocol.
RC1.11 added receipt-qualified historical reclamation; RC1.12 made optional
cleanup errors nonfatal, but still waited for each provider command's full
watchdog while holding the account worker slot.

The new RC1.12 diagnostics show zero historical proof conflicts in the affected
cleanup snapshots. They show completed BODY reads and receipt checks, followed
by a pending EXPUNGE with no completion for 30 seconds. Repeated completed server
starts take about 98–100 seconds to reach rendezvous and fail immediately there
with SELECTED_LANE_CLOSED. The matching phone selected 20/20 sockets and failed at
REQUEST after 90 seconds. This is a different, now observed failure chain from
the hypothetical conflicting-UID bugs repaired in RC1.12. The provider's internal
reason for stalling EXPUNGE is not exposed by these traces.

Two ownership mistakes compound that provider behavior: LoginParallelism limits
whole account jobs, including cleanup, instead of actual login operations; and
socket keepalive starts only after all account jobs finish. RC1.13 moves the
existing login bound around DialSelected and starts the existing NOOP owner as
soon as a service socket is selected. Account inventory can overlap within the
original two-sockets-per-account and endpoint budgets. Every temporary inventory
socket closes before that account's service pair opens. Keepalive owners join
before the next startup owner receives a socket; cancellation closes owned
executors before joining detached issued NOOPs.

No production cleanup proof, original journal locator, reservation, wallet,
request deadline, command watchdog, login concurrency setting or DNS behavior
changes. The repair neither disables cleanup nor turns provider timeout into
deletion or refund authority. The new fixture deliberately retains historical
debt when the provider supplies no EXPUNGE completion or absence proof. Current
session application traffic is tested separately after historical cleanup.

The release evidence records the old failure and corrected startup, plus the
scope of each source, native and packaged-server test. An actual old published
server is included in the comparison; a local fixture is not live-provider
confirmation. Existing performance failures remain visible and unqualified.
