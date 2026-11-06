# ITOM 1.1.0 MAILSTREAM-RC16 — mobile delivery testing candidate

RC10 remains the first field-ready baseline. RC16 is not field-qualified.
Real-provider 100 Mbps mobile performance and VLESS parity remain unverified.

RC15 mobile traces expose a 512 KiB delivery allowance with 0.8–1 second bulk
receipts: approximately 4–5 Mbps despite all thirty carriers remaining ready.
Small setup responses establish a faster baseline than full DATA/FETCH traffic.
Cleanup and physical capacity are not the bottleneck in this capture.

RC16 probes capacity when the flight allowance blocks queued bulk work. It
learns the byte-weighted median of complete saturated receipt rounds, then
retains their minimum baseline. Fast fragments cannot label the entire path;
queue growth cannot enlarge that baseline. Independent send/delivery intervals
protect the rate estimate against compressed receipts. The mobile rate horizon
is bounded at 1.75 seconds, with the original 512 KiB starting flight.

Small interactive DATA and native PING/PONG receipts independently detect
shared access-link queueing. Slow interactive receipts temporarily drain bulk
using a 750 ms rate horizon. A slow full-body FETCH alone cannot train this
signal. Both DATA readers fetch up to thirty-two available containers without
waiting to fill a batch or buffering an entire command. New runtime fields
expose the saturated-round baseline, selected horizon and interactive guard.

Matched full TLS/IMAP models on source eb3db98:

| Model | Upload Mbps, first / second | Download Mbps, first / second | Maximum loaded RTT |
| --- | --- | --- | --- |
| Faster small setup, delayed full DATA | 39.3 / 41.1 | 35.8 / 36.3 | 931 ms |
| Always-delayed DATA stress case | 40.9 / 45.2 | 32.7 / 36.4 | 968 ms |
| Home, 60 ms APPEND service | 130.8 / 130.4 | 205.2 / 181.3 | 505 ms |
| Corporate, 35/50 Mbps up/down | 24.3 / 24.4 | 35.7 / 35.5 | 1,318 ms |

The mobile models use a 95/80 Mbps up/down wire fixture, variable network delay,
60 ms APPEND service and separate 500/600 ms upload/download body FETCH costs.
Frozen RC15 falls to 4.15 Mbps upload and 3.64 Mbps download in the faster-setup
model, then times out before its second pair. Missing reference measurements
remain explicitly absent. Both candidate pairs complete with all carriers.

Three short MULTI runs deliver 115.4/114.7/111.5 Mbps upload and
191.0/196.8/186.8 Mbps download. Every rolling two-second interval starting after
second two exceeds 100 Mbps. The two-second ramp target remains unmet. The
mobile 45/40 Mbps upload/download goal also remains unmet in some phases;
both misses are retained in machine-readable results. Cold persisted-debt
recovery is separate and can limit early throughput until capacity is audited.

The final downloaded server passes three startup/reconnect rounds each with
ten and fifteen accounts, reaching every configured carrier (30/45 roles).
Manual stop resets every local account-capacity entry to zero. All twelve matched
jobs and independent acceptance pass on the exact source, including normal/race,
SDK, pinned-fork, Android tests/lint and weak-link cancellation. The shared 7 Mbps
case completes at 4.74 Mbps upload / 4.54 Mbps download before cancellation,
then establishes fresh HTTPS. Maximum loaded RTT is 1,432 ms and idle/post
stays below 98 ms. Eleven additional focused race cases pass.

These are generated fixtures, not a real-provider or physical Android speedtest.
RC15 isolated-request behavior, priority feedback and two-writer cleanup bound
remain. Physical retention, memory limits, profile format, DNS policy, theme,
application ID and persistent signer remain unchanged. No manual mailbox cleanup
or profile reset is required.

Update both endpoints. Install the signed APK in place; extract the server .bin
into /tmp and use manager option 18. Version code: 10141. Verified download links
and complete final validation are recorded with the exported artifacts.
