# ITOM 1.1.6 RC1 synthetic RTT qualification models

Reference measurements supplied by the user:
- Home 500 Mbps: **3 ms ICMP echo RTT** to `imap.rambler.ru`.
- Home IMAP service/command RTT is modeled as 15–30 ms from the previous constraint.
- Mobile effective IMAP RTT is modeled as 30–150 ms.

ICMP echo from a provider host is NOT equivalent to the time from APPEND
publication to another reader's UID FETCH and back. They have different
network, server-processing, mailbox-visibility and application components.
The fixture therefore runs **three independent schedules**, not a single
confused latency setting:

1. `home-icmp`: 3 ms synthetic client network RTT, a physical floor
   calibration. This deliberately has no additional IMAP command-service cost,
   so it must not be interpreted as predicted real ITOM RTT.
2. `home`: 15/20/30/18/27/22 ms synthetic effective transport RTT, a
   conservative 15–30 ms IMAP-path scenario. It is not an ICMP measurement.
3. `mobile`: 30/45/85/150/70/120/50/95 ms client transport RTT, with an
   independent 160 Mbps down/90 Mbps up serialization limit.

The provider's synthetic server endpoint has its own repeating RTT schedule.
Each scenario uses a separate fixture with measured end-to-end TLS echo RTT,
recorded in the result's `IdleMS`, `LoadedMS`, and `AfterMS` arrays. The
`SYNTHETIC_RTT_REPORT.json`, `SYNTHETIC_RTT_REPORT.csv`, and
`SYNTHETIC_RTT_REPORT.md` files report count, minimum, median, p95,
p99 and maximum in milliseconds **for each phase and each version**. The
percentiles are nearest-rank; small samples cannot prove real-world p95.

GitHub Actions prints all measured values in job logs. The release workflow
repeats all three profiles and packages raw arrays, manifests and reports as
`ITOM-1.1.6-RC1-synthetic-rtt.zip`, with SHA-256 verification. The 200 Mbps
hard floor is enforced for each direction of the high-capacity HOME synthetic
candidate, but actual field acceptance still requires repeated phone/home
measurements, safety, tail-latency analysis, and compatible Android signing.

## RC1 regression acceptance

Before a candidate can be published, the synthetic test gate requires actual
application RTT samples in idle, loaded, and post-load phases for both the
untouched 1.1.5 reference and candidate. It rejects candidate median increases
above max(12ms, 12% of reference) or p95 increases above max(20ms, 15% of
reference) for idle/post-load and max(30ms, 15%) for loaded. Both directions
must sustain >=200 Mbps on high-capacity HOME scenarios. These are early
screening tolerances, not proof of field performance or p95 below 100 ms.

Initial ranked-account routing was **not enabled in the deliverable** because
paired tests showed loaded RTT tails worsening. The scheduler implementation
remains in source for isolated experiments; production packet placement uses
the proven 1.1.5 round-robin path while the NOOP-free control receiver is
qualified separately.
