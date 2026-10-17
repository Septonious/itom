# ITOM 1.0.0 RC3 — bounded response latency candidate

RC3 shortens empty polling backoff during the existing bounded latency-sensitive response window from a maximum of 80 ms to 20 ms. Visible backlog still drains immediately; normal active/quiet cadence, bulk batching/flight, connection counts, quota and retry rules remain unchanged. Persistent IMAP connections and RC2 stall routing remain intact. Higher polling frequency in those short windows is the tradeoff.

The supplied RC2 traces show control RTT minimum 216.21 ms and median 345.83 ms across 209 complete echo pairs. The user reports sustained high speeds and no failed Speedtest runs. These are RC2 observations, not new RC3 measurements. Sub-200 ms and unchanged real-provider throughput require field confirmation; no metric or RTT display is altered.

Focused race checks, normal MULTI/pause/blackhole/correlated-EOF/post-load recovery, 89 Android tests, lint, native checks, targeted vet and 45 launcher checks pass. See VALIDATION.json and TEST_EVIDENCE.zip for exact scope. The historical RC2 instrumented startup-discovery timeout remains unresolved; full internal and real-device/provider qualification are outstanding. RC3 remains an unstable testing prerelease.

Update both endpoints and preserve accounts, profiles and durable state. APK version code 10002 retains the existing app ID and signer. Includes APK, Linux AMD64/ARM64 binaries, executable sources, analysis, handoff and hashed evidence. Actions verifies and publishes the exact locally compiled artifacts; it is not an Actions compilation or a live performance run.
