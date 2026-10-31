# Durable retention and isolated maintenance checkpoint

The final local source gate passed for normal and race builds of core,
mailstream and native, plus the pinned IMAP library suite and vet. The report
records exact runtime fingerprints. No provider performance is qualified.

The mailstream cohort now includes nine HTTPS sessions: deletion available,
refused, and held indefinitely. It recreates the client runtime between sessions,
checks recovered debt, and checks that changing routing aliases retains the
same physical-account debt. Each session uploads/downloads 4,325,376 bytes;
each scenario also checks a complete 1 MiB response after TCP half-close.

Core evidence runs the actual native SOCKS and IPv4 TUN paths. The first native
process receives SIGKILL after successful HTTPS, bypassing Client.Close. A
fresh process reopens its durable state and completes the same application
paths. The child inherits race instrumentation when the parent is instrumented.
Two configured identities remain within the ten-connection provider limit;
a second identity receives authenticated BUSY and a same-user restart works.
Deleting metadata from an existing fixture store fails before network startup
and leaves the original file unchanged.

The retained failure before the final source gate came from a fixture requiring
positive whole milliseconds for a valid ping: sub-millisecond loopback RTT
rounded to zero. Presence is now established by acknowledged samples and a
sample timestamp. No application deadline or performance ceiling was changed.

All 93 actual domain tests pass in an isolated JVM build. This does not compile
an Android APK or qualify a device. The private byte-accounting files are new;
legacy wallets/journals are never opened or reset.

Remaining release blockers: durable ownership records for reclaiming prior
sessions, control/discovery-log retirement, recovery after app data clearing,
and realistic variable-latency/sustained-bandwidth qualification. The current
store preserves prior-session debt rather than guessing that mail disappeared.
This checkpoint publishes no testing RC or APK.
