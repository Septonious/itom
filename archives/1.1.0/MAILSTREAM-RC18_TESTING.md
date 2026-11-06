# ITOM 1.1.0 MAILSTREAM-RC18 — paced bulk and DNS burst handling

RC17 field results were 52.5/17.2 Mbps download/upload with 265 ms idle RTT and
66 ms jitter, versus VLESS 61.8/36.3 Mbps, 54 ms and 8 ms. Both endpoint traces
show shared-link queueing and repeated interactive guard activation. Android
also recorded 27 DNS worker refusals while idle DNS associations filled its pool.

RC18 spaces learned bulk traffic in MIME-sized bursts instead of immediately
publishing its whole measured flight allowance. Interactive/control packets
bypass pacing. The original flight controller, shared-link drain guard, first
512 KiB allowance, memory limits, account limits and wire format remain. A fresh
bulk burst returns to the initial allowance after idle, rather than dumping the
previous speedtest's large flight onto an unmeasured path.

Fresh, successful A records answer fake-DNS questions from the existing bounded
TTL cache. Repeated names use read-only mapping lookups. Idle local UDP DNS
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
