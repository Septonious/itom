## ITOM 1.5.0 RC19 field test

Android adds global routing, per-app Auto, Direct and VPN choices, and bundled GeoSite, GeoIP and blocklists. App routing changes rebuild the VPN interface while keeping the IMAP session running.

The Android ARM64 APK uses the same signing certificate as RC18. The server installer targets Linux AMD64. Built from source revision `95390c2` and transport checkpoint `a1e4d31`.

The eight-account 500/500 Mbps model passed the short and burst tests. Sustained throughput reached 350 Mbps down and 345 Mbps up. Peak sustained control RTT reached 207.6 ms against the 200 ms ceiling. Sustained RTT and physical-device qualification remain open.
