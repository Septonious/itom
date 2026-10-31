# Mailbox stream transport: replacement implementation

This directory is the active rewrite on `codex/v1.1.0-mailbox-stream-v3`.
RC1.13 and the abandoned probe changes are not its runtime. The old development
branch is preserved for recovery and evidence. Nothing here opens, migrates,
deletes or refunds old wallets, grants, journals or transport mailboxes.

## Why the previous transport is being replaced

The real-provider diagnostic reproduces UID EXPUNGE hanging for the entire
30-second command lifetime after successful APPEND, BODY verification and STORE.
The transport then turns one mailbox's maintenance failure into a global
permission failure. Earlier changes only moved that dependency between initial
startup, the first DATA exchange and the next VPN restart. The old adapter did
not establish a successful deletion baseline in that diagnostic: its probe
stopped during MIME verification. That is not evidence that UID EXPUNGE works.

## Replacement contract

* A fresh Noise NNpsk0 handshake establishes a new ephemeral session, with the
  configured per-client 32-byte PSK and protocol identity bound into the transcript.
  Bidirectional encrypted application traffic establishes readiness.
* Every account is independent. Any healthy account can carry startup. There is
  no elected anchor, global CONTROL grant, wallet-history reconciliation or
  mailbox cleanup prerequisite for opening a connection.
* Standard go-imap owns the parser and literal grammar. There is no replacement
  hand-written IMAP response parser. UIDPLUS and custom flags are not protocol
  requirements. UID EXPUNGE is never a delivery primitive.
* One permanent request/response log per direction discovers sessions. Every
  session has separate directional DATA mailboxes, so a reconnect does not scan
  retained DATA from previous sessions. Control discovery is bounded and uses
  UIDVALIDITY/UIDNEXT hints only as cursors, never as authentication.
* Runtime TCP streams have independent offsets, receive windows and lifecycle.
  Slow consumers cannot block the receive loop for other streams. Session-wide
  memory and in-flight bounds provide backpressure without a global stop/wait
  grant exchange. ACKs report actual delivery; they do not refund legacy quota.
* Small writes dispatch immediately. Bulk uses bounded chunks and parallel
  account workers. Measured delivery and queue occupancy can adjust bulk
  concurrency; an isolated RTT increase must not collapse a bulk window.
* Cleanup is an independent owner with a finite physical retention budget.
  Completed session/segment mailboxes can be deleted only after their receiver
  has drained and unselected them. No failed deletion is treated as success.
  An exhausted physical budget eventually stops publication on that account;
  a storage limit cannot be fixed by pretending that mail disappeared.
* Three connections per endpoint/account are the explicit maximum: control
  receive, DATA receive and APPEND writer. No runtime reconnect storm or extra
  cleanup socket is permitted. Failed accounts are isolated; retry policy must
  be separate from an active transfer and preserve that budget.

## One integrated rewrite, not another chain of RC patches

The implementation covers the authenticated envelope, reliable message delivery,
per-stream TCP forwarding, standard IMAP backend, account supervision, independent
maintenance, Android bridge and diagnostics as one design. These are implementation
areas of one replacement, not separately shippable transport RCs. The old branch
must not become the fallback data path.

The first commit implements the new protocol and stream engine and a standalone
IMAP prototype. Android integration, sustained physical reclamation and rollout
qualification remain explicit unfinished work until their code is present.
No APK or testing RC is qualified by a compiling protocol core.

The standalone `cmd/itom-mailstream` command supports a server and a loopback
SOCKS5 TCP client. It uses a new JSON profile (`identity`, a base64 32-byte
`psk_base64`, and `accounts` containing `id`, `host`, `port`, `username`,
`password`). This is not the existing APK's profile format. Its prototype storage
bound is 64 MiB of attempted physical mail per account per Link lifetime;
restarting the process does not prove or refund prior storage. It must not be
installed as a replacement service while durable retention is unfinished.

The first whole-application acceptance run exposed an IDLE ownership bug on
the second session: closing an old DATA socket did not wake its local IDLE waiter.
The corrected owner explicitly wakes and joins it before opening the replacement,
and distinguishes that intentional switch from a carrier failure. The same
scenario then passed all three session starts, each with 4,325,376 HTTPS bytes
up and down. Loopback startup/RTT measurements are not mobile/provider results.

## Acceptance and release boundary

Only whole-path scenarios should drive promotion: verified TLS IMAP, actual TCP
and HTTPS forwarding, simultaneous directions, repeated user restarts, account
loss, stalled maintenance, receiver backpressure and application silence. Test
the built client/server entry points rather than substitute grants or a controller
formula. Retain failures. Synthetic bandwidth is not real-provider bandwidth.

The targets remain maximum 300 ms idle/light-load RTT (250 ms later if validated),
maximum 2000 ms under load, and sustained 100 Mbps in each direction for client
IMAP latency varying 10–50 ms and server IMAP latency varying 20–40 ms. There is
no fixed 2 ms or 20 ms server timing assumption. Startup/TLS failures, stream
corruption and account reconnects under load are not acceptable release results.

The existing app's final direct-DNS policy remains the product requirement:
31.131.251.83 and 185.22.234.229 raced over UDP/TCP 53, 250 ms total timeout;
optional IMAP fallback only after both fail, sticky until the user restarts VPN;
no DoH/DoT. This protocol implementation must not silently change that policy.
