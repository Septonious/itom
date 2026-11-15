ITOM 1.3.0 RC3 addresses three additional independently reproduced startup/reconnect failures after RC2 was reported not to connect.

- Accepts provider FETCH bodies whose UID follows the literal, without unbounded buffering or guessing between multiple pending commands. Requested UID and authenticated body checks remain mandatory.
- Retries a definitively rejected bootstrap APPEND or an incomplete literal after closing its exact socket. A fully sent body with a lost result remains pending and charged.
- Pages retained credit checkpoints instead of blocking startup after 50 heads. Only each exact persisted sent page is forgotten after its authenticated receipt; subsequent pages drain without reinstalling Noise.

Diagnostics now include immutable bootstrap/rendezvous, pending/known-position, anchor, namespace and plan metadata without keys, body contents or blocking on mobile I/O. Dynamic allocation, live users, server-only cleanup and RC2's 31-socket configured capacity for 15 accounts remain intact.

Regression tests reproduce failures on the previous implementation and verify the corrections, including a reconnect with 63 retained checkpoints, bidirectional data, refusal retry, and conservation after a stored APPEND loses its reply. Carrier/core/dynamic multiuser race suites, bundled IMAP tests, native SOCKS/TUN HTTPS/process restart, Android tests/lint, saved signing identity and artifact hashes are checked.

The user confirmed RC2 still failed; the attached field traces are RC1. Fresh diagnostics are needed to establish the remaining provider-specific cause. This candidate has not been field-qualified for connection, RTT or bandwidth, and makes no throughput/latency guarantee.

Update both endpoints. Preserve profiles, identities and journals; no purge/reset/reimport is required. Preexisting genuinely unknown APPENDs are never erased. Only the signed arm64 APK (org.itom.client, code 10305) and static Linux amd64 binary are release assets.
