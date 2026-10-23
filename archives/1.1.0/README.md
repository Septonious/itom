# ITOM 1.1.0 DEV6 experimental testing RC

Current `source/` fixes historical byte-headroom starvation, independent fast refill and known-dead fallback DATA publication. Reproduced quota-bound startup now reaches 30/30 directions across fifteen real TLS/IMAP test accounts while preserving every historical charge. Stable 1.0.0/RC2 remains unchanged.

[Testing notes and downloads](DEV6_TESTING.md) · [Build checks and hashes](DEV6_BUILD.json) · [Field analysis](experiments/dev6-byte-headroom/FIELD_ANALYSIS.md) · [Final matched results](experiments/dev6-byte-headroom/RESULTS.md) · [Retained failures](experiments/dev6-byte-headroom/KNOWN_FAILURES.json).

Final-source sustained qualification is **19/20 versus RC2 19/20**; short sequences are **48/48 versus RC2 42/48**. Direct DEV5 comparison passes 36/36 phases on each version, with measured mean bandwidth differences from −2.7% to +5.7%. Thirteen source manifests were checked. Simulation rates above 200 Mbps do not predict Rambler field throughput.

Full normal Go/vet and accounting races pass. Of 37 selected tunnel race tests, 36 pass and one retains its failed 1100 ms mixed-load chat deadline. CPU-contended capped-socket stress also has 6/30 failures. Experimental packaging explicitly retains those limitations; this is not an all-checks-passed or fully qualified release. No qualification threshold or test assertion was changed. Real-provider idle RTT improvement is unverified.

Update the signed APK in place and preserve app wallets and server journals. Public DEV5 and DEV6 APKs share the persistent signing certificate. Keep the experimental app and server versions paired.
