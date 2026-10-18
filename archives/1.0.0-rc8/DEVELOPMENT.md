# ITOM 1.0.0 RC8 — Development baseline
Date: 2026-10-18

**Status:** in development, **NOT release-qualified**. Preserve RC7 16 MiB server / 12 MiB client flight ceilings, source commit 2b08127305530b2e16ee3c05f669ad8331a6d559, IMAP grants and journals, recovery/session identity, and Android package **org.itom.client** without suffix.

## Evidence driving RC8
- Android field results: SINGLE consistently poor, MULTI highly variable, approximately 17.2/14.1, 86.8/67.4, 49.7/11.8, 27.2/9.57 Mbps, with ping 301-387ms, jitter 28-112ms.
- Snapshot shows all ten accounts ready in ten-account deployment; per-account RTT 315-506ms and IMAP operation latency 6-10ms. A separate screenshot briefly shows a carrier RTT of 165ms, which is an opportunity to prioritize that path but does NOT establish achievable full-end-to-end 165ms RTT.
- Original server/client trace archives, 2026-10-18, retained for account-by-account timeline correlation. Do not diagnose provider failure based on one snapshot or one stalled run.
- The historically successful 16/12MiB run reached 141.8/79.4 Mbps, loaded max RTT 395ms but repeated strict full-capacity + fresh/retained + low-bandwidth tests **FAIL**. Prior 4MiB, adaptive, fast-fair, 352KiB batch and 10-writer variants also failed one or more mandatory gates.

## RC8 stage 1
- Add bounded **fresh positive RTT spike cost** to *interactive* carrier ordering. Continue honoring idle RTT/physical slot/grant eligibility. Never exclude an account, reassign in-flight bytes, mutate authenticated credit, or alter journal and quotas based on RTT.
- UI: darken the full Material3 surface container palette, improve contrast and cyan/violet/green/orange differentiation. Theme admin Sections menu, remove default dropdown gray, and use one gapless progress bar with no Material3 stop indicator.
- Test the penalty (staleness, magnitude, zero/negative samples, tie selection and physical fallback), Kotlin contrast and progress clamping, and original transport field/recovery tests.
- Later work: investigate SINGLE logical ownership versus physical striping, actual per-account latency variation over time, low-bandwidth startup and throughput tails. Changes require independent A/B evidence before promotion.

## Hard acceptance contract
- Package ID **org.itom.client**, versionCode 10007, no debug app-ID suffix.
- Full-capacity 60-second sustained and trailing 30s and 10s **upload >=75Mbps, download >=75Mbps**; maximum loaded RTT **<=750ms**, no missing/failed probes, positive duplex every second, original TCP socket identities survive.
- Fresh+retained and 4+32 stream tests, separate genuine 10->20->50->150 Mbps startup and 150->low downgrade, journal grant recovery and race tests, Android unit/lint and signed JNI builds, Linux amd64/arm64, real user Android/Rambler field validation.
- Do not publish RC8 release tag while ANY gate is red.
