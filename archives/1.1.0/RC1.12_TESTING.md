# ITOM 1.1.0 RC1.12 — retained inventory and startup diagnostics

The uploaded RC1.11 client selected all 20 IMAP connections and waited at REQUEST.
The matching server repeatedly failed during retained-mail inventory, before
rendezvous. RC1.11 recorded only LOCAL_OR_SESSION_FAILURE, which does not identify
the exact underlying field error.

Two reproducible RC1.11 bugs produce this startup abort: an original local BODY
bound to a different UID in the same mailbox incarnation, and duplicate copies
under distinct UIDs. RC1.12 quarantines conflicting cleanup candidates and keeps
the original physical debt charged. Duplicate copies cannot cause one copy to
be deleted and its debt refunded while another remains. Authentication, journal
scope and actual deletion/absence proof remain mandatory.

After a complete authenticated inventory, refusal or loss of optional cleanup
commands no longer prevents the already planned service connection from opening.
Incomplete/unauthenticated inventory, cancellation and poisoned/corrupt journal
failures remain fatal. This does not increase quotas or add reconnects under load.

Passive progress, inventory completion and failures now include the exact phase, fixed error
classification, proof-conflict counters and selected IMAP command/close snapshot.
No provider text, credentials or payload is logged. These diagnostics distinguish
a journal conflict from BODY parsing, authentication, socket loss or command timeout.

Update both endpoints in place. Run the newly downloaded server executable,
choose option 11, then option 9. Fresh trace revision must be itom-1-1-0-rc1-12.
Install the APK over the current app and preserve app data, profiles and journals.
Do not clear data or reinitialize the server to test this update.

The exact field cause was not exposed by RC1.11's logs, so these fixes address
reproduced regressions without claiming live-provider confirmation. Export both
full diagnostics during a remaining failure; the new inventory snapshot will
identify where it stops. Sustained 100 Mbps and the full 300 ms RTT goal remain
unqualified. DNS settings and original application/startup deadlines are unchanged.

Final functional checks passed 319 core groups and 14 native groups under the
race detector, 101 Android unit tests, and all seven packaged-artifact groups,
including 20 persistent VPN restarts. Source light-load results were 64/64
exchanges within 300 ms (maximum 277 ms), and 3/3 after application silence
(maximum 232 ms). The upper-latency scenario failed the full target: 10/16 were
within 300 ms, maximum 335 ms. These are local TLS IMAP fixtures; Android-device
and live Rambler operation have not been qualified.
