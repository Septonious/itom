ITOM 1.2.5 RC3 DEV11 testing build. Install both files: Android ARM64 APK and Linux AMD64 server.

Repairs false account delivery debt from handshake packets and independently confirmed route announcements, plus the queue-admission registration race. The bandwidth controller and server-only mailbox cleanup are retained.

Verification: 195 mailstream, 64 core and 29 native top-level Go tests pass normally and with the race detector; 112 Android unit tests pass; release lint, app identity, native revision and APK signature checks pass. Variable 30–100 ms command-delay regressions pass five repeated race runs. Real-network RTT/throughput qualification remains pending.

App: `org.itom.client`, version `1.2.5-rc3-dev11`, versionCode `10263`. Signed with the saved ITOM release key.

SHA-256:
- `itom-dev11.apk`: `c0e8d8b83eef25ae13cb488aa755d989b3ed89592d1d6ae61ba1ea2893a31315`
- `itom-dev11.bin`: `b04ae8c7d72d7f18156c20fce50ef17e715d9bbcb0360a8e7a33f96d6f3668aa`
