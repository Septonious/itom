# Future worst-case mobile connectivity profile

User-provided direct 4G measurements, without ITOM, reported19October2026. The user considers this approximately the worst realistic access condition; normal conditions should be substantially better.

- Speed:1Mbps download /2.5Mbps upload (interpreting the supplied1/2.5 in download/upload order).
- Ten ping samples to imap.rambler.ru, milliseconds:84,63,43,67,23,60,55,56,21,66.
- Mean53.8ms; median58ms; observed range21–84ms.
- These are ping measurements, not authenticated TCP/TLS/IMAP command timings. Do not substitute them directly for command-processing delay or tunnel RTT. Ten samples do not establish a long-run loss or jitter distribution.

## Intended future qualification

Model this as a client-access condition, independently of the healthy server's approximately20ms Rambler route and strong uplink. Apply asymmetric client capacity:1Mbps provider-to-phone,2.5Mbps phone-to-provider. The current symmetric-cap fixtures need explicit directional support before this can be honestly called a replay of these measurements.

The user's goal in this condition is minimal connectivity suitable for text messaging, approximately0.1Mbps useful application throughput. Test short request/reply completion, continued useful delivery, and absence of permanently dead connections; preserve original application sockets across recovery. Do not require75/100Mbps on a1Mbps access link. Exact duration, directionality of the0.1Mbps goal, and low-capacity latency deadlines have not yet been specified; do not claim these are agreed numerical gates.

Keep this connectivity profile distinct from normal sustained-speed qualification. Normal throughput/tail/latency thresholds remain unchanged. Preserve all quota, journal, unknown APPEND, proof, wallet and identity rules. Do not add a provider-wide blackout or slow-server assumption to represent this phone access condition.

This is a recorded requirement for future simulations only. No simulation was executed and no RC10 source, APK, binary or checksum was changed in response to this note.
