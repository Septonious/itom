# Session integration foundations — session-1

This stage implements SOCKS ingress, coordinator startup, reliable final grant
retirement, Android SOCKS settings and live server logs. It does not connect
`serve` to a production mailbox session runner or expose a usable CLI SOCKS
command. The existing CLI profile remains `itom-imap-probe`.

## SOCKS frontend

core/socks owns a loopback-only TCP listener. ListenAndServe accepts Endpoint
(port, authentication, username, password), validates the selected nonprivileged
port and fails if it cannot bind; it never silently chooses a different port.
Serve also accepts an already bound loopback listener for lifecycle ownership.
The caller injects OpenFunc, which MUST obey its context and return only after
an authenticated remote OPEN_OK. This package contains no direct destination
dial or resolver. It cannot accidentally bypass ITOM when the tunnel is down.

Supported subset: SOCKS5 CONNECT, IPv4 and canonicalized ASCII/A-label domains,
NO AUTH when authentication is disabled or username/password when enabled.
BIND, UDP ASSOCIATE and IPv6 requests receive explicit failure. Authentication
required means method2 only; advertising method0 as well cannot bypass it.
Names/passwords use the RFC1929 1–255 byte bound. Stored comparison digests are
fixed-length SHA256, both compared in constant time; logs contain no credentials.
The protocol source is RFC1928 and RFC1929:
https://www.rfc-editor.org/rfc/rfc1928.html
https://www.rfc-editor.org/rfc/rfc1929.html
This is the stated subset, not a claim to implement all mandatory RFC methods.

No buffered reader consumes application payload accidentally during pipelining.
Two 32KiB pumps preserve n>0 with error, handle short writes and half-close each
direction independently. EOF in upload does not discard a later download.
Stream.Close must be idempotent, safe concurrently with I/O, and unblock both
Read and Write. CloseWrite must order FIN after already queued bytes. OpenFunc
must return the remote bound address for the SOCKS response when available.
Default limits:128 connections,10s handshake,45s OPEN. Cancellation closes
listener and streams and joins handlers. Slow handshakes cannot create unbounded
workers. Idle established streams live until the owner/session closes them.

## Android settings

Settings has one SOCKS5 subpage. Public settings contain port/authentication only.
SocksCredential is stored inside the existing AES-GCM AtomicFile/Keystore vault;
password and username are not added to PublicConfig or diagnostics. Secret
objects/drafts have redacted toString. Drafts are memory-only, not saved into
Bundle/SavedStateHandle. The screen hides password input and applies FLAG_SECURE.
Validation uses UTF8 byte counts; port1024..65535. Enabled auth requires both
credentials. Save uses the existing atomic encrypted update and revision check.

AppSettings schema3 migrates to4 in the vault and settings import; legacy vault1
migration is preserved. Default port1080, authentication disabled. Existing
profiles/keys/routing are retained. An older Android build cannot read schema4;
do not downgrade the APK after saving these settings. No unencrypted migration
file is created. Android versionCode7002/versionName0.7.0-dev2.

The Android backend is still CORE_NOT_INTEGRATED. These are persisted settings,
not a running listener. The eventual bridge must read the matching settings and
secret in one vault snapshot and configure core/socks.Endpoint. Do not report
that the port is listening merely because saving succeeded.

## Authenticated startup

New wire controls COORDINATOR_INFO/ACK (types4/5) carry immutable store ID, journal
run and features=1 (explicit remote-grant retirement supported). They must be
inside the current Noise epoch, after the activation gate. Bootstrap is an
actor-owned state machine with generation checking, bounded retransmission and
45s deadline. A pinned store cannot silently change; unknown features fail.

Client Receive returns Proposed without making the session ready. The metadata
worker opens wallet.OpenBound(path, store, epoch), durably matching wallet identity
to authenticated store/user/current Noise session. Only then CommitStore emits
ACK and enables the client; the server enables only after matching ACK. Lost ACK
is recovered by retransmitting the same offer and a freshly sealed ACK. No
wallet secrets or keys are carried in this offer. Wallet rejects reuse of the
last recorded session or any outstanding grant's original session; callers
still MUST generate a fresh cryptographically random session on each reconnect.
This is a startup contract, not a full production negotiation/session runner.

## Final grant retirement

Previously a fully cleaned closed grant disappeared before its final GC status
was delivered. That could strand the wallet after losing the reply. Now:

1. The coordinator retains the remote grant with Used=Registered=Released and
   Closed=true. It occupies bounded metadata, zero storage debt. Repeated release
   or RemoteStatus returns the exact durable final status, also after restart.
2. Wallet.Acknowledge persists Retired and retains the key/final counters in its
   existing bounded grant map. Retirements returns the durable outgoing requests.
3. GRANT_RETIRE (type114) requires the authenticated owner and exact final state.
   RetireRemote removes ONLY completely closed/registered/cleaned remote metadata.
   Open, uncertain, different-owner or mismatched counter requests cannot delete it.
4. GRANT_RETIRE_ACK (type115) lets the wallet remove that entry. Lost ACK is
   recovered by repeating GRANT_RETIRE even after both processes restart.

An ACK for already absent metadata means only "no matching entry remains";
it is NEVER a cleanup proof, a quota refund or authorization to spend. It cannot
remove a wallet entry unless that wallet already durably accepted final GC status.
It does not change balances. All side effects retain fsync and transaction gates.
Server protected metadata shares and wallet MaxGrants bound pending retirements;
no deadline discards unresolved debt. This does not solve unknown APPEND without
locators or full retired-namespace reconciliation.

Local-only control/probe journals keep schema2. First remote-grant issue/update
raises schema to3 before returning, preventing older binaries from deleting
retained final statuses. Open accepts journal1/2/3, conservatively retains debt,
and migrates local schema1 to2. Wallet schema1 migrates transactionally to2 and
records a fresh session; old writers reject the new wallet format. No DB is reset.

## Live server logs

Menu10 executes journalctl --unit=itom.service --follow --lines=0 --no-pager
--output=short-iso with stdout/stderr streamed directly to the terminal. It does
not collect a finite Output buffer. A scoped SIGINT context stops the viewer;
Ctrl+C returns to the menu without touching the systemd service. Outside that
view ordinary process signal handling remains unchanged. Non-Linux hosts receive
an explicit systemd-unavailable error. New entries appear when the service emits
logs; an idle healthy service may have no new messages.

## Validation and remaining integration

The release evidence records exact Go tests/vet/race results. New tests cover
SOCKS auth/no-auth downgrade rejection, unsupported requests, pipeline payload,
half-close, cancellation, bind conflict; startup persistence ordering/pinning;
retirement after lost finalGC/lost retireACK and two endpoint restarts. Existing
TLS IMAP/DATA tests include the new retirement handshake. Control messages in
these harnesses are authenticated in memory; a provider-backed control lane is
still absent. Kotlin migration/validation tests are provided for local Gradle.

Next: bounded mailbox control lane, account-wide connection/rate governor for
10 accounts shared by two users, worker/result queues and read pumps connecting
these components to serve and client OpenFunc; RESET/rekey/reconnect handling,
then Android backend and real provider traffic/soak. None of these missing links
may be substituted with direct networking or unjournaled DATA APPEND.
