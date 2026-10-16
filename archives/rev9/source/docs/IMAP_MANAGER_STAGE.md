> Исторический документ qualification-этапа. Текущий Android runtime и команды описаны в README.md и ANDROID_RUNTIME_V1.md.

# ITOM 0.7.0-dev4 — IMAP qualification and server manager

This revision adds a runnable **IMAP qualification service**, not a completed
Internet tunnel. It performs a real Noise NNpsk0 handshake and encrypted
PING/PONG through actual IMAP APPEND/FETCH. A TCP exit library and DATA engine now exist (DATA_PATH_V1.md), but are not
connected to this service. There is no live SOCKS listener, TUN or Android backend. The existing UI continues to report CORE_NOT_INTEGRATED.
The qualification profile deliberately has kind `itom-imap-probe`; it is NOT an
Android invitation and must not be imported as one.

## Build locally

From Windows PowerShell, next to the downloaded executable source ZIP:

```powershell
python .\ITOM_PHASE3_SOURCE_0.7.0-dev4_runtime1.zip server --expect-revision runtime-1
```

This automatically extracts, runs Python/Go checks and vet, then builds:

- `dist/itom-linux-amd64` for an x86_64 VPS.
- `dist/itom-linux-arm64` for an aarch64 VPS.
- `dist/itom-windows-amd64.exe` for the client-side Windows qualification.
- `dist/SHA256SUMS` for those locally produced files.

`core` runs the checks without producing server binaries. `--race` is optional
and needs a local C compiler; the ordinary Windows command does not need one.
Go version/dependency pins and sums are verified. VCS stamping is explicitly
disabled for this ZIP distribution, which has no Git history. It does not
change the source revision check or disable test/vet gates.

No APK or distributable server binary was built in the development environment.
The new entrypoint was compiled as part of Go tests. Cross-target packaging is
implemented and its command construction/failure paths tested; the user-run
`server` target remains the validation of the actual Windows/Linux binaries.

## Install/update on the VPS

Manually copy the matching locally built binary to `/tmp`. On Linux x86_64:

```bash
chmod 755 /tmp/itom-linux-amd64 && sudo /tmp/itom-linux-amd64 install && sudo itom
```

For aarch64 substitute `itom-linux-arm64`. The installer writes
`/usr/local/bin/itom` atomically and installs `itom.service`. It preserves
`/var/lib/itom`. An already active service is restarted; immediate restart
failure attempts to restore the previous binary/unit. This checks service
startup, not successful IMAP authentication, which remains visible in the log.
First installation enables the unit but does NOT start an unconfigured service.
The update/rollback and systemd operations have not been exercised on a real VPS
in this environment.

The current qualification unit runs as root with NoNewPrivileges, PrivateTmp,
ProtectSystem=strict, ProtectHome and a write exception for /var/lib/itom. It
opens no inbound network port. A dedicated service UID and hardened production
exit policy are still required when the TCP server is integrated.

## Interactive command: itom

Run `sudo itom` with no subcommand. A persistent numeric menu offers:

1. Configuration summary and systemd status.
2. Initialize the server and its one provider pool.
3. List/add/remove mail accounts, change passwords, purge all folders/mail.
4. Add/rename users, enable/disable a user.
5. Export a selected user's CLI qualification profile.
6. Verify TLS, LOGIN and the user's dedicated folders on all accounts.
7. Start the service.
8. Stop the service.
9. Restart the service.
10. Follow new service log messages live; Ctrl+C returns to the menu without stopping the service.
11. Install/update this executable and the service.
0. Exit.

For the first setup: 2 → add 10 accounts using 3 → create two users using 4 →
check accounts using 6 → export each profile using 5 → start using 7. Account
IDs start at a01; retired IDs are reserved and the active limit remains 10. All accounts share one provider and, in this first
manager schema, the same IMAP hostname; later accounts inherit its port/TLS
mode. Providers with several equivalent hostnames can use the same canonical
hostname. Account removal and full-folder purge are available; see ACCOUNT_MAINTENANCE.md.
Removal retains a credential-free identity tombstone and journals. Provider
migration and quota-debt retirement are not implemented.

Each user receives a CSPRNG eight-digit nonzero-leading UID, a separate random
32-byte PSK and an unpredictable pair of dedicated up/down folders. Renaming,
disabling, re-enabling and process restarts do not rotate UID/key/folders.
This stage has the requested 10-account / 2-user limits. It does not partition
the accounts into 5+5. Disabled users remain registered; user removal,
key rotation and namespace retirement are future manager operations.

Stop the service before configuration mutations and start it afterward. The
manager checks the systemd service; do not simultaneously run an unmanaged
`itom serve` process against the same state. The registry uses bbolt transactions
and exclusive locks, mode 0600, mandatory file sync and Unix directory sync.
IMAP passwords are read without terminal echo and are not printed in listings.
The JSON profile intentionally contains account credentials and the selected
user's key; transfer it privately. Export refuses to overwrite an existing file.

## Client-side real mail test

Copy the user's exported profile to the Windows machine. In the directory
containing the built executable:

```powershell
.\itom-windows-amd64.exe probe --profile .\user-profile.json --account a01
```

Repeat with actual active account IDs from the manager as needed (removed IDs leave gaps). Success prints `Noise + IMAP roundtrip OK` and a
measured mailbox round-trip time. The server must be running. The default
client journal is under the user's OS cache directory, isolated by server ID,
UID and account. `--state PATH` overrides it; use the same path for repeats so
uncertain writes are not forgotten. One instance may own a journal at a time.

This is NOT a throughput benchmark. The active qualification loop polls at 100 ms
for a bounded five-second window, falling back to one second when idle. RTT starts
after the Noise handshake and includes polling, APPEND/FETCH and provider processing.
No sustained performance figure is claimed. On a failed run the log/returned
error identifies the stage without exposing passwords or keys.

## IMAP adapter contract

Pinned library: github.com/emersion/go-imap/v2 v2.0.0-beta.8, with its pinned
transitive go-message/go-sasl versions. Official references:
https://github.com/emersion/go-imap/releases and
https://pkg.go.dev/github.com/emersion/go-imap/v2/imapclient . The prerelease
library is selected explicitly; live-provider qualification remains necessary.

- IMAPS or mandatory STARTTLS; TLS >=1.2, hostname and chain verification.
  No insecure-skip-verify option and no cleartext LOGIN fallback.
- LOGIN/app-password authentication in this stage. OAuth-only configurations
  are not supported yet; “generic IMAP” does not imply every provider auth flow.
- Commands are serialized per connection and bounded by context cancellation
  and 30-second operation timeout. Cancellation closes the underlying socket.
- Raw response byte caps, <=1 MiB decoded body, <=2 MiB MIME, streaming bounded
  BODY.PEEK partial literal reads, exact size checks, no unbounded body Collect.
- Paging by UID ranges of at most 16 numbers, explicit UIDVALIDITY tracking;
  a changed incarnation resets scan position, never silently reuses old UIDs.
- Own generated MIME: binary body as base64 with 76-character lines. Subject
  and markers are never sufficient ownership proof. Full decoded digest and
  journal ownership decide cleanup.
- No blind APPEND retries. Any failed outcome after command entry is unknown;
  journal debt remains held until concretely reconciled.
- UIDPLUS/IMAP4rev2 UID EXPUNGE when available. Otherwise only an exclusive
  user-owned ITOM folder, bounded <=4096 messages, and no OTHER Deleted flags
  before EXPUNGE. A single cleanup owner per directional folder is required.
  INBOX is rejected. No arbitrary user mailboxes are accepted by the manager.
- Cleanup re-fetches and verifies exact bytes and UIDVALIDITY, then verifies
  actual disappearance. After crash between EXPUNGE and journal completion,
  a previously observed locator allows a fresh same-incarnation absence proof.
  A missing never-observed APPEND remains unknown; a fresh scan cannot free it.
- Concurrent first-time folder creation is handled by reselecting after a
  failed CREATE (another endpoint may have created it).

## Qualification runtime budget and recovery

One server connection per account, both users served on it. A single running
client test adds one connection for its account; two users plus server use up
to three connections there. No IDLE/read/write connection pools are opened.
Each account reconnects independently with 1..30 second backoff. There is no
production rate/connection allocation protocol yet.

Qualification uses a SEPARATE bootstrap/control reserve: at most 128 KiB and
16 physical messages per endpoint/user/account, with durable local journal
intents before APPEND. For two endpoints and two users, reserve at least
512 KiB / 64 messages per account OUTSIDE the future DATA allowance. No DATA
frames bypass storage grants: this service sends only handshake and PING/PONG.

Server control journals are stable per account/user, not recreated on rename
or enable/disable. Per-process session caches have at most 32 entries/account,
60-second lifetime. Sent qualification messages are reclaimed after 90 seconds,
or on session/process restart once that process's cryptographic state is gone.
Client cleanup runs after success/failure. Only exact journaled content is
removed. No TCP state or payload is replayed from disk. Unknown/no-locator
APPENDs can eventually exhaust this deliberately small reserve; do not delete
the journal or silently reset debt to get around it. Full retired-namespace
reconciliation is still an outstanding transport task.

## Client wallet and remaining integration

`wallet` implements durable acceptance/spend/fence/descriptor acknowledgements.
It is now connected to the DATA writer component, tested across local TLS IMAP
and real TCP, but not yet to the live service runner. See
WALLET_V1.md. The current qualification service uses its explicit control reserve,
not the DATA wallet. The new authenticated startup component binds coordinator store identity
before accepting grants; live session-runner integration is still pending.

Startup/store binding and repeatable final grant retirement now exist as components
(SESSION_STAGE_V1.md). Still required: no-write reconciliation,
production session runner/worker queues and control lane, real connection/rate
governor, terminal/rekey lifecycle, IDLE/batching/scheduling,
Android backend, provider tests on the user's actual accounts, fault/soak and
throughput qualification. The user has confirmed two live Rambler probe successes
(2.359 s and 1.291 s) with the preceding server revision; new polling timings and
DATA throughput have not been measured there. Do not present this revision as a working VPN.
