# RC15 mobile delivery-window audit

The supplied mobile comparison reports ITOM at 4.56/26.4 and 14.1/20.3 Mbps
(download/upload), against VLESS at 76.1/91.8 Mbps. Idle latency is 273–305 ms
for ITOM and 50 ms for VLESS. The direct tunnel establishes that the access link
can perform better; it does not measure the independent mail-provider route.
Raw exports remain private. Older rotated revisions are excluded below.

All ten accounts and thirty roles remain ready. There are no account failures
or cleanup quarantines. Receive buffers are almost empty and no retention cap
is reached. The client's approximately 136 MB of recovered debt stays deferred
with ample headroom; the server begins with no recovered debt. Cleanup does
almost no work during the measurements. This is not the earlier housekeeping
writer-exclusion or physical-capacity failure.

During the first download, the server holds its delivery allowance at 524,288
bytes, has queued packets and reports flight-limited operation. Bulk receipt RTT
is approximately 719–1,022 ms, while the minimum retained from setup traffic is
358 ms. Half a MiB per 0.8–1 second limits delivery to about 4–5 Mbps, matching
the screenshot. The second transfer briefly grows to about 1.7–2.3 MB, then
contracts repeatedly. Upload flight is also small for its bulk receipt latency.

RC15 permits byte-clocked growth only below minimum bulk RTT plus 250 ms. Its
rate target uses that same short baseline. A capable link with slower full-DATA
FETCH callbacks therefore cannot escape its initial flight: the controller
measures its own limit, treats bulk callback delay as congestion, and reinforces
the low rate. A short setup response can establish this baseline even though
interactive packets themselves are excluded from the estimator.

The candidate allows a rate-based probe when real queued bulk work was blocked
by the flight allowance. The first prototype caps probe horizon at 1.25 seconds.
Individual growth steps are byte-clocked or capped by bytes just acknowledged;
a common measured-rate ceiling bounds slow-RTT probes. Independent send and
delivery intervals still guard against compressed receipts. Initial flight,
physical capacity, memory caps, connection count, controls, cleanup and DNS
policy remain unchanged. The chosen horizon is included in runtime diagnostics.

An independent FIFO/batched-callback regression reproduces the plateau at
5.1 Mbps on frozen RC15 despite 80 Mbps capacity. The initial controller change
reaches 78.4 Mbps in the final five seconds of both 20-second transfers. A true
7 Mbps path delivers 6.9 Mbps with peak allowance approximately 1.1 MB and
maximum bulk RTT 1.3 seconds. Existing compressed-ACK, short-repeat and slow
receipt regressions also pass. These controller simulations are not full IMAP
or real-provider qualification. Full TLS replays and safety checks follow.

The first full TLS mobile hypothesis delays every DATA FETCH by 500/600 ms
(upload/download), including small setup responses, on a 95/80 Mbps wire path.
Frozen RC15 achieves 15.0/19.9 Mbps upload and 18.8/19.5 Mbps download; its
last download times out. The 1.25-second prototype completes all transfers at
28.1/41.7 Mbps upload and 22.8/25.1 Mbps download, but fails the declared
45/40 Mbps mobile acceptance thresholds. This model has slower setup than the
field capture and does not reproduce the exact 512 KiB field plateau.

The client DATA reader also retained an eight-container FETCH fallback where
the server reader used thirty-two. Both now fetch up to thirty-two *available*
containers, without waiting for a full batch or buffering a whole command. A
real TLS regression reads forty-eight ordered containers in two body commands
and then receives an isolated tail; frozen RC15 needs six commands. This fixes
a potential download command ceiling, but the full replay shows no material
improvement from this change alone. Its available batches are usually smaller.

A 1.75-second measured-rate probe ceiling is under qualification. It retains
250 ms of the two-second bulk latency budget. The 7 Mbps FIFO/batched-ACK models
reach 6.9 Mbps with peak allowance about 1.6 MB and maximum RTT 1.8 seconds.
Full-path weak-link loaded RTT and cancellation recovery still must pass.

The 1.75-second local replay completes at 42.2/46.6 Mbps upload and
35.8/41.3 Mbps download. Spending already measured probe headroom on individual
receipts yields 43.4/47.5 Mbps upload and 37.1/36.9 Mbps download. The initially
declared 45/40 Mbps upload/download goal is not met in every phase. It remains
an explicit reported goal flag, rather than a claim of qualification. The
release safety/progress gate requires all transfers, thirty carriers, unchanged
idle/loaded limits, no repeated-run collapse and at least 1.5x each matched
RC15 phase. Home 100 Mbps and weak-link gates remain mandatory and unchanged.

A separate full-path fixture delays DATA only after a large MIME container is
published in that account's selected folder. This preserves faster small setup
responses, unlike the always-delayed stress model. Both frozen and candidate
use identical fixture settings, and both models are retained.

The faster-small-setup full TLS replay reproduces the field-scale collapse on
frozen RC15: upload reaches only 4.08 Mbps, download 3.47 Mbps and the first
download times out before the second pair can start. Aborted reference phases
are preserved and labeled; missing second-run reference measurements are never
invented or copied from another phase. Candidate repeats must still complete
and retain at least eighty percent of their first-run rates.

The final local faster-setup candidate completes both pairs at 42.2/46.5 Mbps
upload and 34.1/39.4 Mbps download. Maximum idle/post RTT is 239.8 ms and loaded
RTT 1,071.7 ms; all thirty carriers remain active. First-pair improvement is
10.3x upload and 9.8x download against the aborted RC15 reference. This remains
a generated TLS/IMAP fixture, not a real-provider or VLESS parity result.

The independent application acceptance run passed on source 9717272. Final
matched build source aeb3ca6 has byte-identical active source files. Eight
focused race cases pass locally, including both mobile capacity cases and the
real TLS backlog/isolated-tail reader regression. The downloaded server passes
three startup/reconnect rounds each with ten and fifteen accounts (30/45 roles),
and manual stop leaves all local account capacity entries at zero.

The first matched build on aeb3ca6 is rejected: its corporate replay completes
all transfers at 24–25 Mbps upload and 35–36 Mbps download, but loaded RTT peaks
at 2,674 ms. Idle/post RTT stays below 106 ms. This is a real regression against
the unchanged two-second gate; the failed artifact is preserved, not rerun away.
The wider probe used smoothed *queued* RTT, allowing queue delay itself to enlarge
flight on a short-baseline constrained path.

The revised controller separately records minimum receipt RTT from a genuinely
flight-limited bulk publication. Tiny setup responses cannot establish that
baseline. Both the estimator and ACK-clock probe ceilings use this minimum, not
the increasing queued RTT. A true idle-to-bulk transition relearns the baseline;
a delayed receipt train with bytes already in flight cannot reset it upward.
The minimum is exported as bulk_saturated_min_rtt_ms. The corporate two-second
gate and all other existing safety gates remain unchanged.
