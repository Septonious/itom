# ITOM 1.1.0 MAILSTREAM-RC18 — paced bulk and DNS burst handling

RC17 field results were 52.5/17.2 Mbps download/upload with 265 ms idle RTT and
66 ms jitter, versus VLESS 61.8/36.3 Mbps, 54 ms and 8 ms. Both endpoint traces
show shared-link queueing and repeated interactive guard activation. Android
also recorded 27 DNS worker refusals while idle DNS associations filled its pool.

RC18 smooths measured bulk traffic in MIME-sized bursts only while the existing
interactive congestion guard detects queueing, through the rest of that bulk
burst. It clears after bulk idle. Quiet
paths retain RC17's startup, ramp and repeated-transfer controller. Interactive
and control packets bypass pacing. Flight, memory and account limits, physical
capacity, wire format and the original 512 KiB initial connection allowance are
unchanged. Continuous pacing and fresh-burst credit resets were rejected because
they slowed the slow-provider replay; they are not in this release.

DNS caching follows the useful dnsmasq/OpenWrt pattern: successful answers expire
by their shortest record/CNAME TTL, negative NXDOMAIN/NODATA answers require a
valid SOA and use its negative TTL, and simultaneous equivalent questions share
one upstream race. The reply cache has 1,024 LRU entries and at most 4 MiB of
wire data; the separate existing 1,024-name address cache evicts one expiring
record instead of flushing the working set. No stale answer or transient failure
is cached, and TTLs never extend on a hit. EDNS requests and unknown record layouts bypass reply rewriting and
keep the original upstream wire response; validation and recursion flags are kept separate. A canceled waiter
cannot cancel another caller. Cache hit/miss/shared-query counts appear in native
diagnostics. Caches reset with the VPN session; persistent fake-IP mappings do
not preserve DNS authority. Repeated names use read-only mapping lookups. Idle local UDP DNS
associations release their workers promptly, preserving queued requests and
socket reuse through the TUN. Resolver addresses, primary/backup racing, direct
250 ms deadline, TCP truncation handling and opt-in session-latched IMAP fallback
are unchanged. DNS/TCP worker limits are unchanged.

RC17's compact itom- names, namespace, durable capacity accounting, bounded
cleanup retries, coherent diagnostics, profile, signer and theme are retained.
No legacy migration or manual mailbox cleanup is required. Update both endpoints
to apply pacing in both directions. Install the signed APK in place, upload the
server .bin to /tmp and use manager option 18. Android version code: 10143.

The unchanged thirteen qualification jobs plus matched RC17 pacing/DNS regressions
and a constrained transfer comparison gate publication. Real-provider speedup,
54 ms RTT, VLESS parity and 100 Mbps on a slower physical uplink are not claimed.
Final artifacts retain all strict throughput/ramp misses and modeled results.
