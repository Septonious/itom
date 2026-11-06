# ITOM 1.1.0 MAILSTREAM-RC15 — repeated-transfer testing candidate

RC10 remains the first field-ready baseline. RC15 is not field-qualified.
The sustained real-provider 100 Mbps goal remains unverified.

RC14 field testing still showed late ramp-up and weaker repeated MULTI uploads.
The current candidate bounds cleanup preparation as well as mutation, applies
compression-safe delivery feedback in both directions, and grows flight from
actual acknowledged bytes while using bounded minimum-RTT headroom.

TLS-sized small-record bursts become bulk after their first 4 KiB. Adjacent
framing remains bulk for 250 ms; isolated requests on quiet/independent streams
retain immediate treatment. Isolated requests keep two-account hedging, while
simultaneous TLS/OPEN bursts avoid duplicating every publication. Receipts and
WINDOW packets may share two-millisecond priority MIME bursts. The unchanged
512 KiB initial flight is distributed across ready accounts. Physical retention,
shared-memory, MIME and packet-bundle bounds remain unchanged.

At most two writers can be borrowed, including pre-mutation APPEND drains. A
slow drain does not hold the single mutation lease. The existing bounded paged
cleanup, authenticated UID fences, durable reservations and cancellation rules
remain required. No manual mailbox cleanup or profile reset is required.

Short full TLS tests measure complete 10-second SINGLE/MULTI transfers, the first
two seconds, rolling one/two-second delivery, command packing and borrowed writers.
A two-second ramp target is reported separately from the sustained-transfer gate;
remaining misses are retained. Long averages do not establish fast startup.
Cold old-debt recovery is reported separately and may be slower until audits
release capacity. No live provider or physical Android validation is claimed.

Paired administration cards share their row's intrinsic height and preserve the
existing gradients, typography and wrapping descriptions. Android build, unit
tests and fatal lint remain required. Version code is 10140; application ID and
persistent signer are unchanged.

Install the signed APK in place and update both endpoints. Extract the server
.bin into /tmp and use manager option 18. GitHub Actions artifacts provide the
verified downloads; no release page is claimed without successful creation.
