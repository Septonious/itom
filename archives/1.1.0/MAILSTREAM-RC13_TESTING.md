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

## Exact-source verification

Runtime source: `d59dbc47c10673d753daadf8e4c7bfb51f97899d`.
Build run: https://github.com/Septonious/itom/actions/runs/42099145973
Independent application acceptance: https://github.com/Septonious/itom/actions/runs/42099145688
All eight build/qualification jobs and independent acceptance passed without a
retry of this frozen source. Android: 93 domain tests, 8 application tests,
zero lint errors, both ARM JNI libraries. The existing persistent signer is used.

Full-phase application throughput, Mbps (two sequential 30-second cycles where
shown; control/MIME overhead is excluded from the throughput numerator):

| Controlled scenario | Upload | Download | Maximum idle/post RTT | Maximum loaded RTT |
| --- | --- | --- | --- | --- |
| Home model, independent APPEND service cost | 104.01 / 103.50 | 128.84 / 126.02 | 223.62 ms | 580.16 ms |
| Server DATA FETCH delayed 350 ms | 116.03 / 108.87 | 342.46 / 345.55 | 93.72 ms | 268.52 ms |
| Ordinary four streams | 251.32 / 294.29 | 326.08 / 328.40 | 92.95 ms | 584.60 ms |
| Corporate 50/35 Mbps link | 24.45 / 24.43 | 35.27 / 35.14 | 86.86 ms | 1342.03 ms |
| 7 Mbps link, download cancellation and fresh HTTPS | 4.74 | 4.52 before cancellation | 91.92 ms | 1761.43 ms |

All modeled transfers completed their prescribed operation, with 30/30 roles,
zero account failures and zero connection-limit rejections. The 7 Mbps scenario
intentionally cancels its download and verifies new HTTPS traffic afterward.
Ordinary one- and 32-stream tests, retained control history and account metadata
interference also passed. The 100 Mbps full-phase goal passed in the high-capacity
models, including the new costly-APPEND model. It did **not** pass every rolling
five-second window or the two-second ramp criterion in the costly-APPEND and
delayed-FETCH models. Some other models recorded half-second stalls. All target
results are retained in `MAILSTREAM_RC13_BUILD.json`; this is not a claim that
the sustained goal has been fully achieved. Low-capacity models cannot meet a
100 Mbps target.

The frozen RC12 selected-mailbox STATUS and global quarantine failures reproduced
and the candidate fixed both. Repeated HTTPS verified 128 MiB in each direction
across two finite-budget accounts, requiring audited physical storage reuse.
The downloaded amd64 server passed six startup/manual VPN reconnect cycles
(three with 10 accounts, three with 15), actual server restarts, and shutdown
local-capacity reset checks. Server and native source checksums match this SHA.

The two-CPU local workspace's aggregate mailstream race run exhausted its
360-second package budget during the final storage-reuse test; no data race was
reported. That timeout is retained alongside the separate mailstream race run
with a larger package budget. Scenario deadlines and runtime source were not
changed. The complete unchanged hosted normal/race/SDK/fork/vet gate passed;
the local initial aggregate is not presented as a passing full gate.
