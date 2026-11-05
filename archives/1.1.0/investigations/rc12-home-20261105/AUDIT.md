# RC12 home-network throughput investigation

The user reports a 500 Mbps access link and low direct IMAP latency. Field
speedtests are 68.2/38.0, 73.9/32.0 and 95.6/41.0 Mbps download/upload. Idle
RTT is 169–198 ms; upload-loaded RTT 329–437 ms. These are below the sustained
100 Mbps goal, despite acceptable responsiveness.

Both current captures are RC12 with 30/30 roles, 10 healthy accounts and zero
account failures. Each account completes startup on its first attempt. The
startup correction is retained; this is not another connection failure.

Client retained debt starts at 289.5 MB and grows to 533.1 MB. Two accounts end
at 66.6 MB, close to the 67.1 MB finite cap. Server retained debt reaches 544.4 MB,
with only one local DATA segment retired. Reclamation repeatedly pauses at
maintenance_expunge/store_expunge with failed/timeout outcomes. The captures
lack individual mutation/audit command timings; they cannot prove which provider
operation stalls. No unknown deletion outcome may be refunded.

## Correctness and isolation work

RC12 pipelines STATUS on the selected mailbox after STORE/EXPUNGE. IMAP
discourages selected-mailbox STATUS. The same operation is used for DATA repair.
The candidate drains mutation replies, then uses fresh SELECT count/UIDValidity
proof, and uses SELECT for selected-mailbox repair/inventory. The command stage
now distinguishes audit_select. A controlled provider that supports mutations
and SELECT but rejects selected STATUS reproduces the RC12 failure; this is a
provider compatibility hypothesis, not a recorded Rambler implementation fact.

RC12 also quarantines all collection after one account fails. The candidate
keeps a single global mutation lease but fences unknown outcomes to the same
physical account and failed folder. Other physical accounts can collect.
Routing aliases cannot evade quarantine. A healthy account's completion cannot
clear another account's failure. Inactive namespaces can retry only their failed
folder during another active session, avoiding stranded retry ownership.

Both DATA and control-maintenance actors drain ordinary APPEND tags before
acquiring the mutation lease. Empty historical slots use authoritative SELECT
proof without duplicate FETCH/audit round trips. Exact byte caps, authenticated
ownership, finite recovery reserve and unknown-outcome accounting are unchanged.
No folder DELETE, CLOSE, UID EXPUNGE, manual purge or speculative refund is added.

## Throughput work and realistic tests

Old controlled providers mostly modeled TCP latency and byte rate. Earlier real
transaction captures show APPEND around 80–100 ms even while metadata operations
complete around 20–30 ms. A new fixture charges all APPEND commands independent
provider service time (60 ms with bounded per-account jitter), including control.
It can reject only post-mutation selected STATUS. These costs remain independent
of low NOOP/network latency.

With the reclamation correction alone, that model yields 58/92 Mbps up/down.
One-millisecond coalescing of established bulk bursts and fuller striping improves
that to 70/104–108 Mbps. Control and the first isolated DATA publication do not
wait. The client queue's eight packet slots also impose a smaller byte batch for
TLS-sized writes; sixteen slots improve uploads to 91–96 Mbps. MIME size, protocol
bundle count, delivery flight and physical retention bounds remain unchanged.
The latest candidate stripes by actual packet size rather than maximum packet
size, so both TLS-sized and larger writes can fill the same bounded container.

New production diagnostics report completed DATA/control message counts, MIME
bytes, queued bytes and tagged APPEND completion latency. These expose provider
command cost and coalescing without credentials or packet contents.

Frozen RC12 fails the selected-audit and cross-account quarantine regressions;
the candidate passes. A real TLS IMAP/HTTPS test verifies 128 MiB upload plus
128 MiB download across two accounts, exceeding the finite aggregate storage
budget and requiring audited reuse. Carrier loss, integrity errors, unaudited
refunds and attempts at incompatible selected STATUS are rejected.

These are prototype observations. Final exact-source throughput, weak-link,
startup, native, Android and storage checks are required before packaging a test
candidate. RC10 remains the first field-ready baseline. Sustained 100 Mbps and
provider cleanup behavior require field confirmation; failures remain recorded.
