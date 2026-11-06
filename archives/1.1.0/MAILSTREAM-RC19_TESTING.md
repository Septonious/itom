# ITOM 1.1.0 MAILSTREAM-RC19 — diagnostics and control polling

RC18 is the first user-accepted release that reliably meets the transport goals.
Keep its source, tag and binaries as the rollback baseline. The two accepted
measurements were 109/131 and 143/144 Mbps down/up, 153/161 ms idle RTT and
11/16 ms jitter. Field evidence is recorded separately from its original build.

RC19 fixes per-account speed reporting at the core, so tracing, the live screen
and exports share bounded sampling windows. The first sample is marked as
measuring; subsequent idle windows show zero. Stable account IDs preserve custom
names. Live counters work with tracing disabled and cannot replace a stopped
session's saved report. Both native APIs count established application TCP
connections consistently. Distinct tunnel streams are exposed separately;
per-account stream counts overlap because traffic can use several accounts and
include opening/closing tunnel streams. They are not IMAP socket counts.

The selected control reader already performs continuous forward UID FETCH.
It now measures empty FETCH service instead of adding a NOOP every two seconds.
This removes a periodic extra round trip before the next delivery poll. The
one-millisecond idle floor, body streaming, UID-hole handling, cancellation,
DATA IDLE/repair, writers and cleanup are retained. IMAP latency is the empty
control poll's service time, not an end-to-end tunnel RTT.

RC18 congestion pacing, startup/ramp controller, DNS cache, compact itom- names,
capacity reset and bounded cleanup are retained. No UDP transport or other new
feature is included. UDP over TCP is possible and deferred. Sub-100 ms RTT is an
aspiration, not a verified result; real IMAP storage/delivery still takes time.

The thirteen existing qualification jobs are retained. A further job compares
three predetermined matched constrained-transfer pairs against RC18 and reports
latency and payload, retaining every attempt. New regression tests exercise real
IMAP polling across the old probe boundary, rates, observer windows and reset,
and native/UI/export count semantics. Android compilation, unit tests and fatal
lint are required. The candidate remains for field testing until retested.

Update both endpoints, install the signed APK in place (version code 10144),
put the server .bin in /tmp, and use manager option 18. No manual cleanup needed.
