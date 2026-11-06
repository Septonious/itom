# ITOM 1.1.0

ITOM 1.1.0 final uses the field-accepted RC18 transport. The RC19 control-polling
change has been reverted; the control polling and bulk controller match RC18. The account-speed, account-name and connection-count diagnostics fixes
are retained. Large DATA generations now use at most 16 MiB for their first
collection page, before an oversized whole-folder EXPUNGE can time out. Refusals
retain the existing 4 MiB retry pages. Incoming proofs with unknown byte totals
are inventoried first; partial progress keeps the entire charge until the final
empty-folder audit. The two-writer bound, mutation deadline and wire format are
unchanged. A successful large generation can finish in at most three bounded
mutations under that deadline, with a timing margin before another slow page.

Control-message growth is bounded independently of bytes: 2,048 ordinary-message
reservations plus 128 recovery reservations per outgoing control folder, counting
queued, in-flight and uncertain APPENDs. Existing remote counts are loaded by the
startup STATUS command, so a restart cannot erase the guard. Ordinary writes
pause when cleanup cannot keep up; audit-proven collection releases reservations.
During quiet periods, control tails of 32 messages are eligible for collection;
DATA folders retain their 128-message bound. Backpressure is visible in diagnostics.
The server must stay running for background cleanup. Empty owned folders may
remain because provider DELETE can lock an account. Unrecognized legacy
folders are not automatically deleted; automatic collection stays in the
proved-owned namespace and preserves other mail.

No new transport feature or speculative performance tuning is
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
