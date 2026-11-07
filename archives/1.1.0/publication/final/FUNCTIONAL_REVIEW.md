# ITOM 1.1.0 functional review

The final release restores RC18's control polling and congestion controller.
The release changes DATA collection, bounds control-message growth, retains the
account-rate and connection-count diagnostic corrections, and ships the Android
release variant. No UDP transport or speculative latency controller is added. Bulk feedback shares bounded 32-millisecond publications; idle feedback and interactive dispatch keep their existing paths.

| Area | Qualification and inspection |
| --- | --- |
| TCP transport | Noise authentication, framing, integrity, half-close, bounded stream admission, concurrent HTTPS, cancellation and a fresh request after cancellation; normal and race Go suites. |
| Repeated MULTI | Three full upload/download pairs with independent APPEND service and a nine-second refusal above 16 MiB EXPUNGE; exact RC18 reference retained. Full-phase payload rates and subsequent-run ratios are checked. |
| Mailbox collection | Exact UID/UIDVALIDITY ownership, bounded byte pages, count and empty audits, refusal and timeout recovery, unrelated-mail preservation, old-session reconciliation and two borrowed writers maximum. A paged turn inventories afresh; an immutable inventory is reused only within one collection lease. Exact UID absence is checked between pages, count/validity at the end of each turn, and an empty SELECT before any credit. Returned writers cancel their FIFO lease claim; rotating account order preserves fairness among ready requests. Normally one publishing connection is borrowed; a slow pre-lease drain permits the second after 500 ms. The original two-writer and single-mutation bounds remain. Directional pressure includes the current incoming generation, with fair alternation when both directions need service. |
| Message growth | Queued, pending, unknown-result, tagged and startup-remote control messages share a count cap. Ordinary traffic pauses at 2,048 reservations; recovery has 128 additional reservations. Peer announcements share the cap. All existing retirement proofs use the finite recovery reserve ahead of ordinary traffic, so the cap cannot strand those proofs. Restarts recover the remote count. Quiet control tails are collected while connected. DATA folders hold at most 128 messages. |
| Service management | Stop/restart/update/purge reset local capacity; profiles and bindings survive. Live-store reset is refused. systemctl has a deadline. Upload selection checks actual binaries, handles architecture and supports rollback. The final amd64 binary is exercised through six native startup/stop cycles across 10- and 15-account configurations. |
| Accounts and clients | Configuration validation, secret handling, invitation import/export, management authorization, server notices and rollback state are covered by Go and Android domain tests. Destructive full-account purge remains an explicit manager action; automatic collection stays within the owned transport namespace. |
| DNS | Positive and negative TTL aging, query identifiers and flags, shared misses, bounded eviction, cancellation, resolver racing and TCP retry for truncated replies; cached bursts avoid IMAP/storage work. |
| Android policies | Profile/vault migration, per-app selection, routing, Wi-Fi/mobile policy and SOCKS credentials; domain tests plus application adapter tests. |
| Diagnostics | Stable account IDs and per-account byte/rate snapshots, distinction between native TCP connections and tunnel streams, startup failures, tracing and archive export. Owner-canceled control readers report normal closure; unexpected active cancellation and protocol errors retain their failure status. JNI identities and embedded diagnostic fields are checked in both shipped ARM libraries. |
| Production package | Package org.itom.client, version 1.1.0/code 10145, release compilation/tests/lint, compiled non-debuggable APK validation, existing signing certificate, both Linux architectures, exact source fingerprints and checksummed exported bytes. |

Automated qualification uses generated local IMAP fixtures and real TCP/TLS
connections. It does not exercise physical Android permission dialogs, vendor
battery policies, the user's server systemd installation, or a live Rambler
mailbox. RC18 is the user-accepted field baseline; the final combination has
separate automated qualification. Sub-100 ms end-to-end RTT is not claimed.

A stopped server cannot perform background IMAP collection. Empty folders may
remain to avoid provider DELETE locks. Unknown legacy folders are not erased
automatically. The new count guard stops further ordinary writes in an owned
control namespace if collection cannot keep up, including after a restart;
it does not claim to remove unrelated historic messages.

Exact results, source/build run identifiers, rejected attempts and all model
misses are recorded in ITOM_110_BUILD.json and ITOM-1.1.0-evidence.zip. Passing
results must be present before publication; this review is not a substitute for
those machine-checked results.

The final source passed all fifteen latest acceptance jobs and independent
source acceptance. One initial startup probe measured 305.647 ms against the
300 ms gate; one unchanged-source job retry passed with startup/post maxima of
250.138 ms for SINGLE and 234.468 ms for MULTI. The original failed artifact,
its hash and the retest reason remain in the evidence. No gate was relaxed.
The exact amd64 package also passed six startup/stop cycles across ten and
fifteen accounts, including empty local capacity entries after stopping.
