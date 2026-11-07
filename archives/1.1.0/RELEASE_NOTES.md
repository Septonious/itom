# ITOM 1.1.0

ITOM 1.1.0 final restores the field-accepted RC18 congestion controller and
control polling. RC19's polling experiment is reverted. Account speeds, account
names and connection-count diagnostics retain their corrections. UDP transport
and a new latency controller remain deferred.

Repeated-transfer cleanup uses byte-bounded DATA pages: at most 16 MiB initially,
then 4 MiB after a provider refusal. An exact consumed-UID proof and UIDVALIDITY
check are required. Every intermediate page proves its deleted UIDs absent
before another page is flagged; a fresh count/validity audit closes each turn.
Only a final empty-folder audit releases capacity. A successful turn reuses its
bounded immutable inventory instead of repeating it between pages. Generation
size remains 128 messages, with the same physical budgets and mutation deadline.

Cleanup normally borrows one publishing connection. A second can proceed after
a slow pre-lease APPEND drain; the hard two-writer and single-mutation bounds
remain. Returning writers cancel abandoned FIFO claims, account scheduling
rotates, and the direction nearest its physical limit receives cleanup priority.
Existing retirement proofs use a bounded recovery reserve ahead of ordinary
writes. Bulk-only feedback batching uses up to 32 milliseconds to reduce message
and cleanup work; idle feedback stays at two milliseconds and interactive work
interrupts batching.

Control-message reservations are capped at 2,048 ordinary messages plus 128
recovery messages per outgoing folder. Queued, in-flight and uncertain APPENDs
count. Startup loads the remote message count, so restarting cannot bypass the
guard. Bulk control cleanup becomes urgent at 1,536 reservations or 1 MiB, leaving
headroom before the hard cap while DATA can reclaim larger reservations.
Ordinary writes pause if cleanup cannot keep up. Quiet control tails of
32 messages are eligible for collection while connected. Backpressure appears
in diagnostics. The server must remain running for background cleanup. Empty
owned folders may remain because provider DELETE can lock an account. Automatic
collection preserves unrelated mail and does not purge unrecognized legacy
folders.

The Android app is a non-debuggable release build: org.itom.client, version
1.1.0/code 10145, with the existing signing certificate. Profiles and credentials
survive an in-place update. Both Linux architectures use the same final source.
Upload the matching .bin to /tmp and use server-manager option 18, then install
the APK over the existing app. Update both endpoints. No profile re-export is
needed.

The final qualification covers normal/race suites, TCP/HTTPS and TUN paths,
manager lifecycle and capacity resets, accounts and profiles, DNS caching,
constrained networks, repeated transfers, mailbox bounds and recovery, Android
release tests/lint, signer identity and exported artifact hashes. Evidence
includes failed development attempts. RC18 remains the accepted real-phone
baseline (109/131 and 143/144 Mbps down/up, 153/161 ms idle RTT). Local models
cannot guarantee another network's speed, live-provider behavior or sub-100 ms
latency. Physical Android permission prompts and vendor power management still
require observation on the device.
