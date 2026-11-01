# ITOM 1.1.0 MAILSTREAM-RC1 — experimental field test

This is the new mailbox-stream transport. Install **both** the server and APK
from this release. It is incompatible with the old session-v2 server/APK pair.
The settings are frozen for startup and real-provider testing.

The APK uses the existing persistent signer and application ID. Update it in
place; preserve app data, server state, profiles and mailboxes. Existing account
credentials and client keys are reused. No legacy wallets or journals are reset.

## Install

1. Run the downloaded Linux binary appropriate for your server, check its
   `version`, then select **11. Install/update service** followed by **9. Restart
   service**. Run the downloaded binary for this update, rather than an older
   copy already installed as `itom`.
2. Install the matching APK over the existing app. Keep the existing profile.
3. Connect once. Readiness requires an authenticated session, a real 16 KiB DATA
   exchange and a measured ping. Check that the account roles populate, then
   open an ordinary HTTPS site before starting a speedtest.
4. If startup or traffic fails, export full diagnostics from both endpoints
   immediately. The client records startup before its first heartbeat; server
   logging/tracing is enabled by default. Preserve the failed session evidence.

## What has been checked

The downloaded server passed ten native startup cycles with 10 and 15 accounts,
including server restarts and preserved client state, reaching 30/30 and 45/45
roles. Native DNS, TCP/UDP 53, named HTTPS, SOCKS/TUN and SIGKILL recovery passed
three independent process runs. APK hashes, both JNI architectures and the
persistent signature were verified.

Local whole-application checks use verified TLS IMAP and HTTPS, normal and race
builds, native SOCKS/TUN traffic, request half-close, clean restarts, SIGKILL
recovery, admission limits and unavailable/stalled cleanup. A 60-second transfer
in each direction completed without account reconnects in the independent
variable-latency model. The frozen short matched comparison measured about
280/313 Mbps, versus RC2's 173/145 Mbps, with light RTT below 215 ms.

Those speeds are **local-model measurements**. Rambler, carrier and Android
device behavior remain to be tested. This build does not claim a real-world
100 Mbps result or a guaranteed first connection on an untested provider path.

This remains a bounded testing prototype. Active-session segment deletion is
independent of delivery and refusal keeps its debt charged. Reclamation of old
sessions, append-only discovery logs and recovery after clearing private app
data are unfinished; this is not a sustained-production release. If a physical
retention budget is reached, publication stops instead of pretending old mail
was deleted. Diagnostics retain the charged byte counts and failure reason.

DNS settings remain unchanged: race 31.131.251.83 and 185.22.234.229 over UDP/TCP
53 with a shared 250 ms timeout; optional IMAP fallback activates only after both
direct resolvers fail and remains active until the VPN is restarted. No DoH/DoT.
