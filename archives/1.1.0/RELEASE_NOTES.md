# ITOM 1.1.0

ITOM 1.1.0 final uses the field-accepted RC18 transport. The RC19 control-polling
change has been reverted; the entire mailstream module matches RC18 byte for
byte. The account-speed, account-name and connection-count diagnostics fixes
are retained. No new transport feature or speculative performance tuning is
included. UDP-over-TCP remains deferred.

The Android app identifies as 1.1.0 (version code 10145), uses the existing
org.itom.client package and signing certificate, and is built as a non-debuggable
release variant. Release shrinking is disabled to preserve the tested native
bridge and serialization behavior. Profiles and credentials survive an in-place
update. The Linux amd64 and arm64 server binaries use the same final source.

Features include encrypted IMAP TCP transport, parallel accounts and lanes,
DNS caching and resolver racing, connection recovery, bounded mailbox cleanup,
local capacity reset on manual lifecycle operations, client/profile management,
server notices and Android administration, per-app/routing/network policies,
SOCKS access, live diagnostics and archive export. Compact itom- folder names,
physical capacity limits and the accepted congestion controller are preserved.

Extract the download, install the APK over the existing app, upload the server
.bin into /tmp and use server-manager option 18. Update both endpoints. No manual
mailbox cleanup or profile re-export is needed.

RC18's accepted phone results were 109/131 and 143/144 Mbps down/up at 153/161 ms
idle RTT. RC19 field results were lower and do not support keeping its polling
change. These are field observations, not a speed guarantee on another network.
The final suite rechecks normal/race behavior, real TCP/HTTPS and TUN paths,
manager lifecycle/account/profile behavior, repeated and constrained transfers,
cleanup, recovery, Android release tests, lint and compiled artifact identity.
Phone-specific system prompts, vendor battery management and live mail-provider
behavior remain dependent on the device/provider. The restored transport is the
user-accepted field baseline; the final packaged combination is separately built
and verified. Sub-100 ms latency and UDP game compatibility are not claimed.
