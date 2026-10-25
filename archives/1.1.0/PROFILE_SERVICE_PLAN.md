# Existing-profile service planning

`PlanProfileService` now maps a validated exported profile to its original,
already-open account/user CONTROL and DATA journals. It performs no network or
filesystem operations, issues no grant, enables no account and changes no journal
profile. Its output is a candidate service/inventory plan, not proof that the
inventory, negotiation or VPN startup has happened.

Installed server state is organized by account and user. Each original file's
store identity and current run remain in the output; separate files cannot be
collapsed into a shared issuer or a new empty database. Journal handles must
belong to the actual configured account, user and provider. Closed/poisoned or
missing scopes are rejected. The caller retains all handles and their original
paths, locks, capacity, unknown attempts and recovery obligations.

The fast lane requires the user's existing `Up` folder in both original ledgers.
A split layout uses the existing `Down-data-00` DATA folder; automatic layout
uses that folder when already authorized, otherwise the existing `Down` folder.
Mixed layout uses `Down`. A missing authorized folder fails rather than creating
one, changing layout silently or extending a full legacy folder set.

Every retained CONTROL/DATA folder for the owner appears in the complete
inventory list, including unused legacy shards. The combined set must remain
within the original ten-folder bound. A four-shard DATA profile with both legacy
folders already uses all ten; adding an eleventh CONTROL folder cannot pass.
Selecting two lanes does not discard the other eight folders or their debt.

Both endpoint budgets are supplied explicitly. Each must cover every selected
account and fit the profile endpoint cap. Each participant reserves its client
and server per-account allocation within the shared provider login ceiling, and
all server allocations together must fit the server endpoint cap. No resource is
inferred from whatever connections happened to succeed. Insufficient capacity
rejects the complete selection; configured accounts are never silently truncated.
The current two/three per-account primary budgets fit a five-session profile for
one participant, or ten sessions for two participants.

`SharedRosterPlan` adapts only scopes which really share BOTH original issuer
identities and runs, returning `V2_PER_ACCOUNT_ROSTER_REQUIRED` otherwise. The
new `RosterSelection` retains all account/folder selections for
`NewServerAccountRoster`, which accepts each original account's journal handles.
[Per-account runtime integration](PER_ACCOUNT_RUNTIME.md) now negotiates those
identities and opens the corresponding client wallet paths in place. The original
shared-store test remains separate from the new twenty-issuer application case.

Tests use twenty distinct original stores for ten accounts, cover all ten retained
folders, reject foreign owners/providers, shared-login/endpoint overcommit and
folder overflow, and verify that planning neither enables inventory-gated
admission nor releases an unknown original shard publication. Raw checks and
source hashes are in [the profile check manifest](experiments/session-v2/PROFILE_PLAN_CHECK_RESULTS.json).
The earlier [roster evidence](experiments/session-v2/ROSTER_CHECK_RESULTS.json),
including its retained intermittent cold TLS timeout, remains unchanged.

Production entry points remain `V2_NOT_READY`. This planner does not supply
inventory/recovery, a long-lived rendezvous owner, production service ownership,
CONTROL/DATA renewal policy or native activation. None of these source checks
establishes the original 250ms/2000ms/100Mbps and zero-failure release gates.
