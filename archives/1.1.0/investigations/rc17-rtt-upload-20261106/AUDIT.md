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

The subsequently supplied Android full export matches the same run (SHA-256
`8abf519525abe07e570494aadc337f1e8ef986a950327f6e8a7629203e966ee8`).
Its 48 RC17 client runtime samples cover the full transfer. Older RC13–RC16
rotations and startup records are excluded from the comparison.

During upload the client keeps approximately 2.3–4.6 MB pending, with the
interactive drain guard active throughout the main phase. Its allowance
contracts from about 2 MB to 512 KiB while as much as 2.2 MB remains in flight.
The measured delivery estimate briefly falls to 4.1 Mbps before recovering.
Client APPEND completion rises from roughly 85 ms idle to 246–430 ms under
upload; its direct provider NOOP also rises. This supports shared-link queueing
and drain/credit oscillation, rather than missing data lanes or full storage.

The native counters additionally record 27 DNS worker refusals and 44 failed
DNS resolutions, with no TCP worker refusal or open timeout. DNS workers reach
63/64, while idle UDP associations retain their worker for one second. The
successful A-record cache is used by application dials but not repeated fake-DNS
answers; repeated mappings also use a write transaction. These amplify DNS
bursts and Android storage/network work. The existing stream cap is reached
with 26 capacity waits; it remains bounded. Counters alone do not identify the
cause of five failed external opens.

The candidate spaces learned bulk traffic in MIME-sized bursts, preserving
control priority, original flight/guard bounds, protocol, storage and account
limits. A fresh burst returns to the original 512 KiB credit rather than the
prior speedtest's large allowance. Fresh A records answer from the existing TTL
cache and established fake names use read transactions. Local idle DNS workers
release promptly; actual TUN tests verify queued queries and reuse of the same
UDP socket after local association expiry. Resolver destinations, 250 ms direct
deadline, fallback policy and worker limits are unchanged.

A prototype matched TLS model (36/80 Mbps directional wire limits, 60 ms APPEND
service, variable delay, sixteen streams, two twenty-second upload/download
cycles) compares frozen RC17 with pacing. Median loaded probe RTT falls from
496 to 342 ms, p95 from 892 to 631 ms and median adjacent probe RTT delta from
128 to 63 ms. Upload phases remain near 24 Mbps in both versions; download
phases change from 55.7/57.4 to 53.9/53.2 Mbps. All transfers complete with
30/30 roles. This probe variation is not Ookla's jitter metric and does not
claim a physical-provider speedup. The final matched build must independently
repeat the comparison and all existing safety/throughput gates.

The initial fixed-256-KiB pacing prototype is rejected: it lowers ten-second
MULTI upload phases to 59–79 Mbps in the unrestricted service-cost model.
A rate-sized burst alone restores download but leaves upload ramp too slow.
The revised candidate permits the controller's existing fast startup when bulk
queue delay is low, retaining conservative pacing under queue growth and the
original interactive drain guard. The unchanged short-transfer gate must pass;
reducing loaded delay cannot justify accepting a throughput regression.

There is also a substantial idle IMAP-path component: client/server direct
command timing and mailbox publication/polling differ from a direct VLESS route.
Pacing targets added queue delay and variation; it does not claim to eliminate
the mailbox relay's entire base RTT or achieve 100 Mbps on a 36 Mbps raw uplink.

The revised local prototype retains the short MULTI full-phase and rolling
throughput gates (three uploads 108.8/104.9/101.8 Mbps; downloads
183.3/170.9/170.5 Mbps). The strict two-second ramp target is still missed and
remains recorded. In the matched constrained model, median loaded RTT is 367 ms,
p95 721 ms and median adjacent RTT delta 85 ms; uploads 24.1/23.9 Mbps,
downloads 55.0/55.9 Mbps. These are model measurements, not field results.

At the user's request, RC18 also adds dnsmasq-style bounded reply caching and
query sharing. NXDOMAIN/NODATA require SOA authority; negative TTL is the smaller
of SOA record TTL and MINIMUM. Positive records expire at the shortest reply TTL,
including CNAMEs. Cached answers are aged and bound to each caller's question/ID.
Timeouts, SERVFAIL/REFUSED, zero-TTL and unqualified empty answers are not cached.
Ordinary reply caching excludes EDNS options and distinguishes validation and
recursion flags. Address and reply caches each have 1,024 entries; only one cold
or expiring record is evicted on pressure. Shared callers retain cancellation
independence. The VPN-scoped cache adds cumulative, content-free diagnostics.
Resolver selection, timeouts, optional fallback latch and worker budgets remain.

The full hosted aa5a1d5 candidate passes the constrained pacing comparison and
all other completed throughput/recovery jobs, but fails the unchanged slow-DATA
mobile comparison: 33.5/22.4/33.2/23.9 Mbps upload/download across two cycles.
It is rejected. Follow-up full-path and MIME-quantum prototypes remain below the
existing upload criterion. Their results are retained; they are not releases.

The production candidate therefore narrows pacing to the existing interactive
congestion guard plus two seconds of recovery, with bounded MIME bursts.
Quiet paths retain RC17's original controller and repeat behavior. The proposed
fresh-burst credit reset and continuous pacing are removed. This avoids
mistaking long provider DATA work for a shared-link queue, and keeps the code
small. The same matched comparison and every previous gate must qualify this
revised source before publication. DNS caching remains independently verified.

A short four-second reactive pacing interval preserves throughput, but the local
replay still reaches 884 ms p95 and a 115 ms median adjacent RTT delta. It misses
the new improvement gate and is rejected. The next candidate keeps smoothing
for the remainder of an actually congested bulk burst, clearing it at the
controller's existing idle boundary. Quiet slow-DATA paths never enable pacing.
The Android export check was also corrected to expect RC18 version code 10143;
the preceding APK compilation/tests/lint had passed before that packaging check.

The burst-scoped version passes the unchanged local improvement gate: loaded
median 406 ms versus RC17's 496 ms; p95 691 versus 892 ms; median adjacent RTT
delta 92 versus 128 ms. Upload is 24.2/24.1 Mbps and download 55.5/57.5 Mbps,
versus 24.5/24.6 and 55.7/57.4. All transfers complete with thirty roles.
The actual speedtest jitter metric and physical-device performance remain
unqualified. The full matched build must repeat this result and all old gates.

The final compatibility review reproduces corruption of compressed pointers in
opaque RP RDATA when a cached DNS message is repacked. Such unknown question
or record layouts now bypass rewriting and retain the original exchange. Known
question replies containing opaque records are not cached, and coalesced callers
fall back to their own unchanged exchange if rebinding cannot be done safely.
The reproducer fails before the fix and passes afterward, alongside a mixed
known/opaque-record case. Resolver addresses, deadlines and fallback remain.
The cleanup checker now distinguishes only normal terminal shutdown rows; three
counterexamples still reject live failures, nonterminal closes and unexpected
close reasons. This is a checker correction, not a runtime cleanup relaxation.
