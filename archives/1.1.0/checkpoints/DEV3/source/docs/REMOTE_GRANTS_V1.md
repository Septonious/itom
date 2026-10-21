# Durable remote storage grants — core-4-grants-1

Status: implemented coordinator-side storage accounting and wire codecs, with
real unit/integration tests. This is not the completed distributed runtime.
The client spend journal, worker/connection governor and real IMAP scanner
remain required before enabling these grants in a transport.

## One authoritative account budget

`journal.Store.Issue` (local server writes) and `IssueRemote` (client writes)
reserve in the SAME bbolt transaction model, balances, user shares and bounded
metadata counters. Do not put `pool.Governor` alongside this store with another
copy of the account's budget. Governor remains an in-memory reference model.

Both users share all ten configured accounts. Their byte/message and metadata
shares are protected on every account and across the pool respectively. One
provider per pool; the code has no provider-specific IMAP assumptions.

These are DATA storage limits. Bootstrap/control reserve, connection allocation,
rate limiting and actual mailbox quota discovery are separate required runtime
components. `StorageGrant` does NOT authorize LOGIN, extra connections or an
APPEND without durable client-side spending.

For a grant:

- `Limit`: immutable lifetime allocation in encoded bytes and physical attempts.
- `Used`: cumulative reported spending, including uncertain effects.
- `Registered`: contiguous prefix of physical descriptors durably recorded by
  the coordinator; includes rows already cleaned and removed.
- `Released`: cumulative confirmed physical cleanup, never a peer's claim.
- `Closed`: authenticated immutable final spending commitment.

Remote invariant: `Released <= Registered <= Used <= Limit`, componentwise.
Held exposure is `Limit - Released` while open, `Used - Released` when closed.
Live remote attempt charges sum exactly to `Registered - Released`.
The difference `Used - Registered` still occupies quota even without descriptors.
Account/user held balances sum local AND remote grants.

Cleanup frees account capacity for NEW grants. It does not replenish the
lifetime spending of the old grant. No timeout, TCP receipt, reconnection,
process restart, failed SEARCH or peer GC counter frees unresolved storage.

## Identity, authentication and generations

The full `wire.QuotaKey` contains random persistent store ID, issuing journal
Run (`Epoch`), monotonic grant ID, original writer session ID, eight-digit user
ID and account ID. Journal Run is not a Noise cipher epoch. IDs never repeat.
A DB replacement has a different random store ID.

`RemoteOffer` returns an immutable offer only in its issuing Run while open and
admission-enabled. Resending it does not call `IssueRemote` again. If an Issue
commit/return is uncertain, stop and reopen, scan durable records and reconcile;
never blindly allocate again. No automatic old-offer publication after restart.

`ApplyRemote(authenticatedUser, run, message)` accepts only UsageReport and
StorageRelease. User MUST come from the Noise session binding, never from the
message. The current metadata job Run rejects old server-worker completions.
The owning actor must additionally check its own session/worker generation.
A newly authenticated session of the same user may reconcile a reference to
its old original session, but may not spend that old allocation.

The actual client wallet must durably deduplicate offers, bind spending to its
original session, fence admission before RELEASE, include all possible APPENDs
in final spending, and retain immutable descriptors for retransmission. These
client obligations are NOT implemented by this coordinator revision. In
particular, new Noise keys after process death must not resurrect old TCP or
unspent old-session grants.

## Wire messages (all stream=logical=offset=0)

All integers are unsigned big-endian. `QuotaKey` encodes:

| Field | Bytes |
| --- | ---: |
| Store | 16 |
| Issuing journal Run | 8 |
| Grant ID | 8 |
| Original session | 16 |
| User, ASCII decimal | 8 |
| Account byte length | 1 |
| Account, restricted ASCII | 1–64 |

A `Quota` is encoded bytes u64 followed by messages u64. Both zero or both
nonzero; bytes <= 2^40, messages <= 2^24, messages <= bytes. These are protocol
bounds, not suggested deployment defaults. Actual per-account limits are lower.

After QuotaKey:

| Type | Payload |
| --- | --- |
| WRITE_GRANT (96) | Limit quota, strictly nonzero |
| GRANT_USAGE (97), aggregate | Total quota, mode byte 0 |
| GRANT_USAGE (97), descriptor | Total quota, mode byte 1, descriptor |
| GRANT_RELEASE (98) | Final quota; zero is valid for a wholly unused grant |
| GC_STATUS (112) | Used quota, Registered quota, Released quota, Closed byte 0/1 |
| GC_PROOF_REQUEST (113) | Next descriptor ordinal u64, 1..2^24 |

Descriptor: Batch ID 16 bytes; encoded size u64; decoded size u64; full decoded
SHA-256 32 bytes; folder byte length u8; exact UTF-8 folder 1..255 bytes.
Decoded size <= 1 MiB, encoded size <= 2 MiB and >= decoded size. Empty/zero
identities, INBOX, control characters, invalid UTF-8, unknown mode/flag bits,
truncation, trailing bytes and ignored offsets fail validation. New typed nil
pointers are rejected just as for existing controls. Seven independently
Python-struct-generated fixtures specify exact frame bytes.

Controls require authenticated Noise records. A valid codec is not authority.
Server-origin WRITE_GRANT/GC_STATUS/GC_PROOF_REQUEST cannot enter ApplyRemote.
GC_PROOF_REQUEST asks for metadata, NOT permission to APPEND or repeat DATA.
Its retransmission scheduler is future actor work. REKEY is still unsupported.

## Descriptor prefix and idempotence

The descriptor's `Total.Messages` is its ordinal. For the next new descriptor:

`ordinal = Registered.Messages + 1`

`Total.Bytes = Registered.Bytes + descriptor.EncodedBytes`

A gap is ErrOrder and changes nothing. Retry from the persisted prefix reported
by RemoteStatus / GC_STATUS. Aggregate usage and RELEASE may arrive before
individual descriptors. Aggregate snapshots are monotonic: older dominated
snapshots are ignored, crossed axes or contradictory same-count snapshots
fail. CLOSED final usage cannot grow or change.

Old descriptors at/below the registered prefix return current status without
creating a row or releasing anything. Current-prefix counters must agree;
older descriptors are no longer revalidated against removed individual rows.
Such acknowledgements mean only that the prefix is already registered, NOT
that arbitrary repeated descriptor content has become authoritative. Current
rows and full-body ownership remain unchanged.

The ordinal prefix survives cleanup and crash. This gives bounded deduplication
without retaining one tombstone per physical attempt. Live row caps and protected
per-user metadata shares still apply. Refusing admission to a descriptor leaves
all counters and bytes unchanged in the same rolled-back transaction.

Closed fully released grants retire. Later messages return ErrMissing without
reallocating or releasing anything. ErrMissing is deliberately not a standalone
proof of cleanup: the future client lifecycle needs bounded retirement/status
reconciliation, including a lost final GC acknowledgement. This revision does
not invent permission to erase uncertain client metadata or spend again.

## Physical binding and actual cleanup

A remote decoded mail body starts with the `ITQ1` wrapper:

`ITQ1 || QuotaKey || ordinal:u64 || batch:16 || encrypted batch (nonempty)`

The complete decoded body, including this wrapper, is <= 1 MiB. Reserve wrapper
space in future batching/MIME sizing. The wrapper is clear metadata, not AEAD or
a new key scheme; it exposes the included routing/grant identifiers to the mail
provider. The opaque body remains governed by Noise. Do not deliver data based
on CheckPhysical: the body needs secure.Open and ordinary session validation.

The complete body's SHA-256 arrives in authenticated GRANT_USAGE. Therefore a
full observed message cannot prove cleanup for two different grant/attempt
ordinals, even if a peer copies another descriptor's digest and batch ID.
The coordinator verifies the exact wrapper binding AND the digest/decoded size.
This is not marker-only ownership or a new cryptographic protocol.

Remote descriptors become Unknown, never Prepared/Started. Local Prepare,
Start, Complete, CancelPrepared and CloseGrant refuse remote records. The
coordinator has no authority to retry a remote physical write.

Trusted scanner Observe must supply:

- Exact configured account and user-owned folder.
- Full bounded decoded body, valid binding and matching SHA-256/length.
- Actual stored MIME size (`Observation.EncodedBytes`), matching the descriptor.
- Current nonzero UIDVALIDITY and UID from the owning mailbox generation.

Only trusted local Deleted after concrete actual deletion/absence of that
previously observed UID in the same UIDVALIDITY releases storage. It shares the
existing transactional cleanup path. A peer cannot call Deleted through the
wire adapter. No generic cumulative peer `Clean` API is introduced.

The future IMAP worker must reject stale connection/mailbox completions and
verify UIDVALIDITY at cleanup. The journal cannot independently inspect a mail
server. A mismatched MIME size is refused, not rounded down or treated as clean.
Shared mailbox credentials cannot prevent a malicious credential-holder filling
the mailbox independently; grants regulate cooperating ITOM endpoints and
protect their configured admission shares, not provider-side hostile isolation.

A remote attempted message that never appeared remains conservative debt until
a future definitive no-write or fenced namespace reconciliation protocol proves
its outcome. This release path, bounded recovery policy and namespace retirement
are still required before production: a single empty scan is insufficient.

## Persistence and migration

bbolt v1.4.3 and existing dependency pins are unchanged. Fsync is mandatory.
Every allocation, usage update, descriptor, final close and cleanup commits
before returning a publishable result. Unexpected errors poison the store until
reopen; no result/permit/ack escapes an uncertain commit. Disk work belongs on a
bounded metadata worker, not the actor loop.

Schema 2 adds Remote/Registered to grants and Ordinal to attempts. Schema-1
local records migrate in the same transaction as audit/recovery; missing new
fields decode as zero. Existing balances, IDs and unresolved attempts survive.
Config mismatch/corruption aborts the transaction. Downgrading a schema-2 DB
into core3 is intentionally rejected, never silently reinitialized.

Open still audits all balances, references, counters, per-user metadata shares
and remote ordinal uniqueness. Admission resets until trusted inventory;
existing remote allocations remain held. Bounded rows are not a hard maximum
bbolt file size; compaction and disk monitoring remain runtime work.

## Tests and boundaries

Tests exercise mixed local/remote allocation on ten accounts for two users,
concurrent issuance, user isolation, reordered final usage and descriptors,
lost acknowledgements, duplicate cleanup across restart, strict scope/epoch
checks, metadata-cap rollback, invalid size/binding, schema upgrade, uncertain
commit callbacks, actual child process exit without Close, codec fixtures and
real Noise + journal integration with fresh keys after restart.

Injected scanner observations are NOT an IMAP integration test. Process exit is
NOT power-loss qualification. Long fuzz, IMAP failures/soak, performance,
Android/native integration and deployment remain future gates. Next: durable
client spend/fencing and retirement reconciliation, connection permits/rate
budgets, actor/workers and full stream/rekey lifecycle, then generic IMAP and
server/Linux end-to-end before Android.
