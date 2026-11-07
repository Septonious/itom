# ITOM 1.1.1

This final patch fixes control-folder bootstrap and feedback distribution when
some mail accounts have not entered service. It retains the accepted RC18
transport controller and every 1.1.0 DATA/control cleanup bound.

Startup inventories only the two owned control-folder names before creating
missing folders. It avoids SELECT or STATUS on absent folders and redundant
CREATE on existing folders. A concurrent peer creation is verified with LIST;
permission refusals remain errors. Diagnostics distinguish LIST, creation,
verification, selection and inventory stages.

Feedback advances from the physical account that accepted the previous burst.
Unavailable accounts no longer concentrate receipt traffic on the first healthy
account. All-healthy scheduling, congestion windows and polling are unchanged.

Update both endpoints: install the APK over the existing app and upload the
matching Linux .bin to /tmp, then use server-manager option 18. The APK uses
org.itom.client, version 1.1.1/code 10146 and the existing signing certificate.
Profiles and credentials survive an in-place update; no profile re-export or
manual mailbox cleanup is required. Check the app and running server both show
1.1.1. The field capture that triggered this patch contained a 1.1.0 app and a
server still reporting RC19, so it did not establish matched-final behavior.

Physical mailbox reservations remain bounded at 64 MiB per account plus a
64 KiB recovery reserve, 128 DATA messages per generation, and 2,048 ordinary
plus 128 recovery control messages per outgoing folder. Queued and uncertain
writes count; restart cannot bypass remote counts. Collection uses exact UID
proofs and byte-bounded pages. Writes pause if collection cannot keep up.
Background collection requires a running server. Empty owned folders can remain;
unrelated and unrecognized legacy mail is preserved.

Qualification includes the complete 1.1.0 gates, frozen-reference regressions,
five empty accounts among ten, and repeated transfers with five unavailable
accounts. Generated TLS/provider models are not live-provider or phone tests.
No new congestion controller, UDP transport, legacy-mail purge or latency target
is introduced. Real-network speed and sub-100 ms RTT remain unguaranteed.
