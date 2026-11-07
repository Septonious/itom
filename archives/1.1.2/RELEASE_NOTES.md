# ITOM 1.1.2

This final maintenance release fixes recovery stalls that could reduce upload
throughput on a second or later transfer. It preserves the accepted RC18
transport controller, control polling, DNS cache and physical mailbox limits.

A failed control-folder collection is retried against the current authenticated
consumption watermark before historical recovery resumes. Previously, an
already-covered startup fence could skip that retry indefinitely, leaving old
DATA charged against the account and removing it from bulk service.

Accounts with substantial inherited DATA debt request recovery before their
remaining headroom is exhausted. A preferred recovery account can no longer
reserve an idle global mutation slot while another account is ready. Healthy
closed historical DATA slots use the existing 16 MiB first-page ceiling to
amortize cleanup; after a failure, the conservative 4 MiB and 32-message retry
limits still apply. There remains only one physical mutation at a time.

No credit is restored until exact UID, UIDVALIDITY and fresh message-count audits
prove the owned scope was collected. Queued and uncertain writes count against
the unchanged 64 MiB per-account budget plus 64 KiB recovery reserve. DATA slots
remain bounded to 128 messages; outgoing control folders allow 2,048 ordinary
plus 128 recovery messages. Writes pause when collection cannot keep up.
Unrelated and unrecognized legacy mail is preserved; no broad purge is added.
Keep the server running for background collection. Empty owned folders may remain.

Update both endpoints: install ITOM-1.1.2.apk over the existing app and upload the
matching Linux binary into /tmp, then use server-manager option 18. The APK keeps
org.itom.client and the established signing certificate, with version code 10147.
Profiles and credentials survive an in-place update. No manual mailbox cleanup,
account reset or profile re-export is required. Check both endpoints show 1.1.2.

Qualification retains startup, degraded-account, weak-link, cancellation, DNS,
normal/race, Android tests/lint, ownership-audit and repeated-transfer checks.
An abrupt phone socket cut verifies server-only cleanup without a final client
acknowledgement, and a 100/1/100 Mbps shared-link replay with variable 30–100 ms client IMAP
path delay checks session recovery. The dip model allows up to 30 seconds for
an application echo; its latency spikes remain visible in the evidence.
New regressions reproduce the published 1.1.1 recovery faults before checking the
fix. A separate TLS/provider replay physically seeds the captured distribution
of old DATA and checks three consecutive download/upload runs, followed by
post-recovery transfers. Raw model evidence and earlier unsuccessful attempts
are included; personal field captures and signing credentials are excluded.

Generated provider models do not establish a live-network speed or RTT guarantee.
No new congestion controller, UDP transport or sub-100 ms RTT claim is introduced.
The source ZIP launcher also handles --help without trying to export a build.

The physical old-mail replay permits one slower initial upload during cold
recovery, as accepted in field review; subsequent phases must sustain 120 Mbps.
This exception is recorded explicitly in the qualification metadata.
