# ITOM 1.1.0 MAILSTREAM-RC13 — sustainable-throughput testing candidate

RC10 remains the first field-ready baseline. RC13 has not been field-qualified.
The real-provider sustained 100 Mbps upload/download goal remains unverified.

RC12 retains 30/30 roles but accumulates over 500 MB of charged storage at each
endpoint, with repeated collection failures. RC13 drains mutation replies, then
uses fresh SELECT count/UIDValidity audits and selected-mailbox repair. A failed
physical account quarantines its own failed folder; other accounts still collect
under the unchanged single global mutation lease. Unknown results remain charged.
Empty historical slots use SELECT proof without duplicate FETCH/audit work.

Bulk stripes follow actual packet sizes. A 16-slot software queue lets TLS-sized
writes fill a bounded container. Only established bulk bursts may coalesce for
an interval based on APPEND service time, capped at 4 ms; control and the first
isolated DATA publication bypass it. Bulk-only receipts coalesce for up to 8 ms
within the unchanged 32-sequence format; interactive/control receipts retain
2 ms and preempt that batch. MIME, wire, physical retention and flight caps are
unchanged. No extra physical connections or larger initial flight is introduced.

Diagnostics add per-account tagged APPEND completion latency, DATA/control
message counts and MIME bytes, queued bytes and account-specific quarantine
stages. These distinguish provider command cost from NOOP/network latency.
No credentials or plaintext payloads are added. DNS and UI settings are unchanged.
There is no mailbox DELETE, CLOSE, UID EXPUNGE or speculative capacity refund.

Install the signed APK in place and update both endpoints. App ID and signer
are unchanged; Android version code is 10138. Extract the server .bin into /tmp
and use manager option 18. No manual mailbox cleaning or profile reset is asked.
GitHub release creation was denied for RC12; verified signed Actions artifacts
are the fallback if that restriction persists.

Qualification includes frozen-RC12 failures, 128 MiB uploads plus 128 MiB downloads
across two finite-budget accounts, costly APPEND service independent of low
network RTT, asymmetric 50/35 Mbps and 7 Mbps links, fresh TLS after cancellation,
manual VPN reconnects, actual server restarts and shutdown capacity reset.
Controlled model results and every failed observation remain distinct from field
measurements. Passing a model is not live Rambler or Android-device qualification.
