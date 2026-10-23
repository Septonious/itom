# ITOM 1.1.0 DEV7 testing package

DEV7 removes shared IMAP command waits and makes two full bulk copies usable where retained quota capacity permits, while preserving a complete FAST batch and every old reservation. It also fixes a concurrent admission diagnostic-snapshot race.

Update **both APK and server binary in place**. Android remains `org.itom.client`, version code `10107`, signed with the same persistent certificate as published DEV5/DEV6. Preserve app/profile data, wallet history, server identity and journals.

Exact runtime: `d4cc33c4db2277be8b93e48a7e3b715032a38b00`.

Matched captured-capacity tests: DEV7 averaged **119.1/91.3 Mbps MULTI** and **116.8/104.1 Mbps SINGLE**; DEV6 averaged 92.7/68.7 and 91.9/69.4. All twelve DEV7 phases completed, with fewer stalled half-second windows. Only 4/12 met every strict 100 Mbps/no-stall criterion. Original sustained gates: DEV7 17/20, RC2 19/20; original short phases: DEV7 48/48, RC2 42/48. All numerical thresholds and failed observations remain intact.

This is an **experimental testing build**. Real-provider Speedtest reliability, consistent 100 Mbps and idle/light RTT improvement remain unverified. The initial instant failure was outside the retained startup trace, so it has no proven diagnosis. See the included RESULTS and KNOWN_FAILURES files for the remaining loaded RTT, tail-speed and stall failures.

## Downloads

[APK](https://github.com/Septonious/itom/releases/download/v1.1.0-dev7/ITOM-1.1.0-DEV7.apk) · [Linux amd64](https://github.com/Septonious/itom/releases/download/v1.1.0-dev7/ITOM-1.1.0-DEV7-linux-amd64.bin) · [Linux arm64](https://github.com/Septonious/itom/releases/download/v1.1.0-dev7/ITOM-1.1.0-DEV7-linux-arm64.bin).
