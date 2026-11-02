# RC4 load failure: audit and recovery checkpoint

Status: investigation incomplete. Last published release remains MAILSTREAM-RC4.
This branch does not change the active runtime or publish an APK.
Base: bbdcbf6bd2329bf2bee395b86d2e552bbb948bc1.

## Field evidence

Both supplied bundles identify itom-1-1-0-mailstream-rc4.
The initial native startup succeeds with all ten control, writer and DATA roles,
and the local capacity reset works. The user reports approximately 170 ms RTT
and throughput up to 50 Mbps, followed by two speedtest errors.

Server trace times below are UTC, as stored in the supplied logs:

- 19:15:02: session established; all thirty client roles become available.
- Around 19:15:43–45: independent maintenance pauses on the first accounts,
  with zero completed maintenance commands.
- By 19:15:57: maintenance is paused on a01, a02, a07, a08 and a09. The other
  accounts continue completing maintenance. Retained bytes are approximately
  58 MB total, far below the aggregate local capacity ceiling.
- Subsequently, RTT on the affected accounts reaches several seconds;
  control receive cursors stop advancing and reliable packets retransmit.
- 19:16:19: the session becomes closed following the user's disconnect.
  Server DATA and APPEND roles remain allocated while teardown waits.
- The next client attempt times out in NOISE. Five client accounts stall in
  control selection and fail; healthy-account handshakes cannot recover the
  blocked server session owner.
- The later manager health check times out on a01/a02 even with the service
  stopped; eight other accounts pass.

The manager's old check is not a pure login test: CheckFolders selects the
legacy user's Up/Down folders. Its empty stage/reason fields do not establish
whether TCP, TLS, LOGIN, CREATE, or SELECT timed out. It must be replaced with
an explicitly labeled authentication/NOOP check, with typed stage and reason.

## What the evidence establishes, and what remains uncertain

An independent maintenance socket does not guarantee isolation from the
provider's account-wide resource or metadata locks. The failed maintenance
pools completed no command, so the existing traces do not distinguish a hung
maintenance LOGIN from a hung first DELETE. Both live folder deletion and the
seventh physical connection are candidates. Do not claim the exact provider
implementation is proved.

The October 31 isolated-transaction upload also matters: the old custom
session executor completed APPEND, FETCH and STORE but UID EXPUNGE timed out
after thirty seconds. The reference adapter stopped during its FETCH check,
so that capture did not prove that the reference adapter's expunge worked.
Consequently UID EXPUNGE must not be reintroduced as a required transport
primitive solely because an in-memory provider accepts it.

## Reproduction completed before the workspace failure

A new TLS-IMAP fixture models DELETE poisoning other connections on the same
account even after the maintenance socket closes. RC4's real duplex HTTPS
request stalls after 6,578,005 verified bytes, with two DELETE attempts; its
teardown then fails to complete before the sixty-second test deadline. This
reproduces the important load-plus-reconnect failure class, but does not
certify Rambler's exact internal locking mechanism.

An experimental reader-owned two-slot implementation using UID EXPUNGE passed
the same 36 MiB upload/download plus immediate reconnect model in 6.56 seconds.
Its full local mailstream normal tests passed in 26.1 seconds, and core normal
tests passed. This intermediate implementation is NOT the release candidate:
the older real-provider UID EXPUNGE evidence rules out treating that local pass
as sufficient qualification.

The workspace went unavailable before the final CLOSE revision could be
applied or tested. The failed execution request must not be assumed to have
modified the filesystem. Uncommitted intermediate changes may still be on the
recoverable workspace.

## Intended final implementation

Keep Noise admission, stream reliability, receipt pacing, DNS policy, the app
theme, signing identity, and RC4's explicit manual capacity-reset policy.

1. Remove live maintenance connections and all automatic folder DELETE.
   Unexpected crash debt remains charged; explicit stop/restart/update/purge
   continues to reset server-local counters as requested.
2. Use two physical DATA slots per direction and session, with a separate
   monotonically increasing logical generation. Never create a folder for
   every generation.
3. The sole DATA reader fetches exactly thirty-two bounded MIME containers,
   retaining their exact UIDs and validating message sizes, sequence numbers,
   UIDVALIDITY, and owned slot name.
4. Only after that slot is completely consumed, mark those exact UIDs deleted.
   Use standard CLOSE on that same selected connection to release the
   selection and remove marked messages. Do not issue UID EXPUNGE.
5. STATUS the old, now-unselected slot and require zero messages and unchanged
   UIDVALIDITY before emitting an authenticated reclamation receipt.
6. The producer returns the exact locally charged generation bytes only when
   the receipt, all packet acknowledgments, and every APPEND tag are terminal.
   It cannot reuse a slot two generations later while the old record exists.
7. A blocked DATA slot must not block that account's control queue or prevent
   other accounts being selected. Existing memory, queue and retention limits
   remain enforced.
8. Reconnect uses a fresh session namespace. Use a fresh control protocol
   namespace for the changed reclamation frame so mixed versions cannot
   silently disagree.
9. Replace menu 6's legacy-folder check with the existing authentication/NOOP
   probe, and fill stage/reason for cancellation after LOGIN or CAPABILITY.

CLOSE is itself unverified on the real provider in this design. It needs
failure-mode tests and field confirmation; do not promise that it works.

## Concrete files to recover or update

Intermediate edits existed under /workspace/work/rc8/repository, based on RC4:

- mailstream/storage.go: two-slot ownership, authenticated reclaimed-generation
  credit and terminal-tag/receipt checks.
- mailstream/imap.go: slot admission, consumed UID sets, removal of maintenance
  ownership and historical automatic DELETE.
- mailstream/protocol.go and session.go: a reclaimed-generation frame.
- core/tunnel/server.go: remove maintenance pools and their physical socket.
- mailstream/provider_lock_test.go: account-wide DELETE poison fixture and
  duplex HTTPS/reconnect regression.
- core/manager/manager.go, core/probe/imap.go, core/mailbox/mailbox.go:
  authentication/NOOP health checks and typed failure details.
- Old maintenance/historical storage code and tests were copied outside the
  active source modules into archives/1.1.0/retired/mailstream-rc4-storage/.

Artifacts and raw user traces were kept locally in
/workspace/work/v110/field-rc4-load-nov02/. Do not commit raw user traces,
credentials, private profiles or signing material.

## Required verification before another RC

- Extend the provider fixture to poison UID EXPUNGE as well as DELETE, while
  allowing ordinary CLOSE.
- Prove the two slots actually wrap, remain bounded, and cannot be reused
  before a delayed APPEND completion or authenticated reclamation.
- Exercise concurrent TLS negotiation and bulk flows near the observed
  thirty-four active streams. The old qualification runner's sixteen-stream
  input ceiling must be increased deliberately for this test.
- Complete repeated duplex transfers, cancellations, immediate VPN
  reconnects, forced process crashes, and the preserved capacity-reset tests.
- Run normal/race/vet/core/native/SDK tests and verify actual compiled-server
  startup/restarts and weak-network recovery.
- Build both JNI architectures; sign using the existing persistent key
  (certificate SHA-256
  47d0d87a99b31e32d7f8b9e5f199d3155c5626c324aa7a29f93c42db1d18e0a6).
- Publish only a matched server/APK pair after artifact verification.

The execution environment became unavailable with an executor-key recovery
error. The environment-status connector still reported running/connected, but
actual command execution returned "No usable execution environment is
available." That status snapshot is not proof that tests or signing can run.
