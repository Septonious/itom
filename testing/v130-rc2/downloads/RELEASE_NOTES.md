ITOM 1.3.0 RC2 fixes a receive deadlock that reproduces RC1 startup failures. A provider can send a pipelined FETCH literal before completing SEARCH; waiting for SEARCH first blocks the decoder that must consume that literal. RC2 drains the bounded FETCH response first, preserving command pipelining and checked UID-gap recovery.

The home screen's old three-sockets-per-account fallback showed 45 for 15 accounts while import correctly showed 31. Home, native startup and import now agree on two DATA roles per leased account plus one endpoint CONTROL anchor. Failed startup traces also retain their final carrier evidence.

Verified: old RC1 fails both the literal-first CONTROL/DATA test and full service bootstrap reproducer; RC2 passes. Full race-detected transport2, carrier, core, dynamic multiuser and recovery suites pass; native SOCKS/TUN HTTPS/process restart, Android unit tests/lint, version identity and the saved signing certificate are checked. These local fixtures do not establish real-provider RTT, speed or connection qualification.

Dynamic leasing, live user registration/revocation and server-only cleanup are retained. Outstanding/unknown mail debt stays charged through timeouts and handoffs. No mailbox purge, journal reset or profile reimport is required.

Update both endpoints together. Assets are only the signed arm64 Android APK (org.itom.client, code 10304) and static Linux amd64 server binary. This is a testing candidate; RC1 and stable releases are preserved.
