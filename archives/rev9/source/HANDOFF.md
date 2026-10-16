# ITOM 0.9.2 Rev9 targeted handoff

## Status

Rev9 is a targeted testing candidate. A complete final internal suite and matching physical Android/Rambler qualification are not claimed. The 100 Mbps speed floor remains strict, without the old ten-percent speed tolerance. `VALIDATION.json` records the exact final measurements and any failed checks. A partial pass must not be represented as a stable release.

Repository: https://github.com/Septonious/itom
Branch: `codex/rev9-targeted`
Release tag: `v0.9.2-Rev9`
Editable source: `archives/rev9/source/`, or `itom/` in the executable source ZIP.
Base: published Rev8 commit 644ad76. Published Rev7 and Rev8 archives remain intact for rollback. Source, progress and subsequent build/evidence checkpoints are pushed throughout development.

## IMAP IP pinning

Each mail account accepts an optional `dial_ip` containing one literal IPv4 or IPv6 address. Blank retains DNS. The TCP socket connects to this address, while `host` remains the TLS SNI and certificate-verification name. TLS validation is never disabled, including STARTTLS. A failed explicit pin does not silently switch to a different DNS backend. There is no automatic IP selection or automatic exclusion of Rambler addresses.

Server manager: stop the service, choose **3. Mail accounts → 7. IMAP IP pinning**, choose one account or all accounts, then enter the address or leave blank to restore DNS. Start the service afterward. The existing account IDs, credentials, journals and physical quota/debt remain intact. Newly added accounts inherit the first account's provider settings, including its pin.

Android: edit the profile and expand an IMAP account to set its pin. **Set IP pin for all accounts** applies or clears the value in one draft operation; Save commits it through the existing vault/restart flow. The authenticated admin tab provides server account/all-account pinning through the existing independent sixty-second rollback watchdog. A server pin and the phone's local pin can be different, because their routes may differ. Changing the server does not silently rewrite an already-installed phone profile: edit the phone or export/import an updated invitation.

Server Android invitation files and import links export nonempty pins; unpinned invitations omit the optional field. The native Android configuration carries the pin unchanged. Profile editing preserves the account's existing STARTTLS mode. Upgrade endpoints before distributing pinned profiles to older apps, whose strict parsers may reject the new field. TLS authenticates the configured hostname, not the pinned IP itself.

Example server account fragment:

```json
{"id":"a01","imap":{"host":"imap.rambler.ru","dial_ip":"81.19.77.171","port":993,"tls":"tls","username":"YOUR_ACCOUNT","password":"YOUR_PASSWORD"}}
```

Use addresses appropriate to your own endpoint measurements. The supplied Finnish route report shows approximately 16–18 ms TCP establishment on four Rambler IPs and rare second-long setup outliers. It does not establish authenticated IMAP operation latency, client-side mobile RTT, steady throughput, or an ITOM performance result on the new server. This work does not migrate a VPS or contact it with user credentials.

## Persistent connections

The inherited transport already reuses authenticated IMAP sockets. Dedicated readers use IDLE; DATA writers, control and cleaner sockets are retained. Idle auxiliary sockets are checked every ten seconds and receive a NOOP after twenty seconds without I/O. Keepalive skips an occupied command mutex or pending IDLE completion. It carries no mail, grant, useful application bytes or proof of quota reclamation. Real connection failures still use paced repair without discarding unknown APPEND debt.

Rev9 targeted TLS and STARTTLS tests verify a single physical connection and exactly one LOGIN across multiple APPEND, FETCH and NOOP operations, including the actual idle keepalive path. Certificate-name failure rejects the pin with no DNS retry. Persistence is therefore an audited existing mechanism, not a claimed newly invented transport. Cold-start setup logins and recovery logins must be distinguished from steady command reuse.

## Transport changes

A diagnostic 60-second retained-state MULTI run and CPU profile on the two-core laboratory host showed repeated full-BODY SHA-256, repeated MIME decoding and actor/scheduler scans. The delivered changes reduce repeated work while keeping the original physical accounting and authorization checks:

- ITE2 payload verification computes one whole-BODY digest and validates the complete immutable descriptor, including account, session, cumulative quota, ordinal, batch and folder, before returning encrypted DATA. The datapath avoids redoing the same proof through separate identity/hash passes. Per-frame Noise/AEAD and replay checks remain mandatory. Legacy ITQ1 verification remains separate.
- Successful BINARY APPEND locator hints reuse the already-decoded physical BODY. Canonical MIME is not decoded again for that hint. The hint still grants no deletion/refund authority; exact fetched-body proof remains required.
- Bulk carrier scores are sampled once per selection, avoiding repeated pool/queue scans from the sort comparator and inconsistent comparisons as queue state changes.
- A Pump progress pass lazily counts active bulk readers once and updates that local count when classification changes. Socket removal invalidates it. The cache never survives an actor/input turn. The per-stream share, logical queue limits, interactive reserve and physical quotas are unchanged.

The original eight bulk workers, bounded speculative queue, uncertain-APPEND treatment and grant rollover/fencing rules remain intact. No latency or provider caps are increased to manufacture a speed pass. Rev8's calibrated fast-delay allowance and temporary missed-IDLE fallback remain inherited.

Before these optimizations, the low-RTT laboratory baseline completed thirty probes and post-load reuse, but achieved only 81.103 Mbps upload / 58.659 download; chronological adjacent RTT variation averaged 129.032 ms. A profiler-instrumented run is diagnostic, never performance certification. A supplementary race-instrumented initial bulk transfer timed out its existing deadline and remains disclosed. Exact final normal/race, Android and uninstrumented load outcomes belong to `VALIDATION.json` and `TEST_EVIDENCE.zip`.

## Rules to preserve

Count every physical APPEND attempt, including unknown outcomes, against durable byte/message limits. Do not replay an already sealed MIME body as a new uncharged attempt, reset counters, discard old debt or manufacture deletion proof. Cleanup remains authorized only by the existing receipt/deletion/fence rules. Cache entries and scheduler signals are not proof.

Keep physical socket caps, fast/control reserves, logical flight and shared handoff queues bounded. Slow journal/crypto/network work stays outside the ordered collector. Cancellation releases an unspent reservation while retaining published uncertainty. A carrier replacement must not conceal an unexpected healthy-load outage in a test. Preserve original TCP identities during physical repair.

Admin authorization remains server authoritative: an enabled client named exactly `admin` needs its own key challenge. Every reversible mutation arms an independent durable watchdog first. Sixty seconds without verified two-way recovery rolls back; sixty continuously healthy seconds commits. Permanent purges, uninstall, journal migration and full account reset stay CLI-only.



## Build and install

Python 3.10+ and pinned Go 1.27.1 are required. Android tooling pins JDK 17, Gradle 9.3.1, SDK 37 and NDK 28.2. Standard build commands execute their full standard gates:

```bash
python tools/build.py server --cache work/build-cache --expect-revision transport-rework3-rev9
python tools/build.py android --cache work/build-cache --expect-revision transport-rework3-rev9 --accept-android-licenses
```

The archived Rev9 release drivers implement the requested targeted finalization and contain workspace/cache paths to adapt on another machine. Do not claim a full suite from these targeted commands. Never run builds, profiling or compression alongside a timed performance run. The final measurement uses the original strict useful-byte and complete-probe checks; provider/session/physical-accounting caps remain unchanged. Any low-RTT command-delay scenario is declared laboratory input, not inferred as a proven IMAP command time from TCP setup measurements.

Update both the app and the appropriate server binary while preserving configuration, wallets, journals and state. The APK retains application ID `org.itom.client.dev.rev7`, uses version code 9209, and the existing signing certificate SHA-256 `39667cd3f91521b09af012b2ca08f8f0ba0d7e07f14b32a9a1e794cb3f4bf999`. Check server `version` reports `transport-rework3-rev9`. The Linux AMD64 executable is for x86-64; use ARM64 for ARM Linux. Private signing/provider credentials are not included.

The full package contains the APK, both Linux binaries, executable complete source archive, this handoff, analysis, validation evidence and checksums. GitHub Actions assembles the exact built bits from hashed parts, verifies source/native identities, uploads a draft, validates every remote asset size and digest, then publishes a testing prerelease. This is assembly and verification of the locally built artifacts, not an unclaimed Actions rebuild.

## Remaining field work

Use matching endpoints to check cold start, repeated SINGLE/MULTI, original Telegram/chat sockets after load, fresh connections, extended idle and Wi-Fi/mobile transitions. Compare DNS and pinned routes separately on each endpoint; do not assume that a good server-side IP is also best for a mobile ISP. Keep raw captures and exported invitations private. The earlier whole-app mobile crash remains unconfirmed without a fatal stack.

If any strict speed, jitter, liveness or required targeted check fails, retain its evidence and keep the candidate unstable. A stable release still needs the outstanding failures resolved and the complete required qualification. Do not discard durable accounting or increase speculative concurrency as a shortcut.
