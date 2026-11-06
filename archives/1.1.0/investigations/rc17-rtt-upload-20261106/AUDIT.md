# RC17 field latency, upload and jitter audit

The field comparison is ITOM 52.5/17.2 Mbps down/up, 265 ms idle RTT and
66 ms speedtest jitter; VLESS 61.8/36.3 Mbps, 54 ms and 8 ms. These sequential
measurements use different transport routes and do not establish a fixed raw
link ceiling.

Both uploaded ZIPs are byte-identical server-only captures (SHA-256
`a4d0e3f63e5a70b40f16d583f17f95060b477801eb2fe8ea0ee34c3abd2387d1`).
They contain 72 RC17 runtime samples at approximately two-second intervals,
a final server snapshot and older RC16 rotations. Only RC17 is analyzed here.
Raw exports stay private; no credentials, mailbox identifiers or user records
are copied into this audit.

All ten accounts/thirty roles remain ready throughout the session. No carrier
failure or cleanup quarantine is observed. The largest account debt is below
15 MB, far below its unchanged 64 MiB limit. Historical collection completes
before the speedtest; the main download runs with no borrowed housekeeping
writer. One later DATA collection completes during upload. The old full-capacity
cleanup failure does not explain this measurement.

The server's interactive receipt path has a minimum near 201 ms, a quiet median
near 272 ms and quiet samples around 213–305 ms. This is transport receipt RTT,
not a substitute for the speedtest's ping or jitter metric. During download,
publication is visibly bursty: approximately 6–7 MB flights coexist with
30–73 Mbps two-second publication rates, and receipt RTT spikes to 1,406 and
1,202 ms. The current flight controller reduces its allowance after these
spikes, then grows again. This supports queueing and bursty publication as
contributors to the loaded delay and jitter; two-second samples cannot identify
individual packet gaps or assign all jitter to one component.

Incoming upload rises from roughly 6.5 to 21.3 Mbps across the sampled phase.
The server's outbound bulk flight is near zero during that phase: its delivery
controller describes download, not the client's upload controller. A server-only
capture cannot tell whether the client is flight-limited, draining the interactive
guard, waiting for stream credit, or blocked on provider publication/reader work.
It also lacks Android underlying-network and client command timings.

Base64/MIME expansion accounts for about 1.37x wire bytes before additional
headers/control. At a fixed 36.3 Mbps raw uplink this would leave roughly
26.5 Mbps payload before other costs, rather than 36.3. The observed 17.2 Mbps
still leaves a meaningful unexplained gap; encoding alone is insufficient.
The direct and IMAP routes also have different base latency. Extra idle RTT
and loaded queueing should be investigated separately.

Next evidence required: the full Android app diagnostics from this same run,
with app logging enabled before connection. Correlate client delivery allowance,
rate, flight, interactive guard, WINDOW credit and per-account publication counts
against these server receive samples. Candidate fixes should target the measured
limiter and publication bursts while preserving RC17 startup, finite storage,
short folder names, cleanup and the existing weak-link safeguards. Do not claim
a 54 ms target or a 100 Mbps uplink from these captures. No runtime parameters
or protocol behavior are changed on the basis of the incomplete sender evidence.
