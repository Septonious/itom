# Verified RC18 testing downloads

All fourteen matched jobs, independent source acceptance and signed export passed.
All fourteen exported files were downloaded again and matched the expected hashes.
The downloaded APK passed v3 signature and persistent-certificate verification;
both JNI libraries matched the build report. RC10's fourteen assets and the RC16
and RC17 reference tags remain unchanged.

- [Signed Android APK ZIP](https://github.com/Septonious/itom/actions/runs/42299498063/artifacts/13543860967)
- [Linux amd64 server ZIP](https://github.com/Septonious/itom/actions/runs/42299498063/artifacts/13542734413)
- [Linux arm64 server ZIP](https://github.com/Septonious/itom/actions/runs/42299498063/artifacts/13542978843)
- [Sources and verification evidence](https://github.com/Septonious/itom/actions/runs/42299498063/artifacts/13544260577)

Extract the ZIPs. Install the signed APK in place, upload the server .bin to /tmp
and use manager option 18. Update both endpoints; no profile reset or manual
mailbox cleanup is required. Android version code is 10143 and the signer is
unchanged.

DNS caching follows the useful OpenWrt/dnsmasq pattern: bounded TTL caching,
SOA-qualified negative answers, individual eviction and shared simultaneous
lookups. Cached answers never extend TTLs. Transient failures and stale answers
are not cached. Unknown record layouts preserve their original wire format.
Diagnostics expose cache hit/miss/shared-query counts. Idle DNS workers release
promptly; the TUN socket-reuse test passes. Resolver addresses, deadlines,
fallback policy and worker limits are unchanged.

Bulk pacing applies after observed interactive congestion, for the remainder of
that bulk burst. Quiet paths retain RC17's startup and transfer controller.
Three independent paired comparisons with identical transport-model source
fingerprints all improve RTT and variation while preserving phase throughput.
Their geometric-mean ratios show about 18% lower median loaded RTT, 23% lower
p95 and 25% lower adjacent probe RTT variation. The initial narrow percentage
miss and every raw comparison are retained, including the independent repeat.
Probe variation is not Ookla jitter and these are controlled model results.

The exact server binary passed six startup/reconnect cycles at 30/30 and 45/45
lines, including verified manual-stop capacity resets. The native race suite
passes 32 tests, plus real process/TUN/reconnect checks. Android passes 93 domain
tests, 8 app tests and fatal lint with zero errors for both JNI ABIs. All strict
throughput/ramp misses remain in the evidence; field performance and VLESS parity
are not claimed. RC10 retains its first field-ready designation.
