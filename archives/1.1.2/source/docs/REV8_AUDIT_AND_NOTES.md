# Rev8 transport audit and development notes

## Findings and changes

| Path | Finding | Implemented behavior |
| --- | --- | --- |
| Receive acknowledgment and credit | Separate RECEIPT and WINDOW records create avoidable encrypted control work for the same stream. | Receive progress uses one cumulative authenticated STATUS snapshot on the existing ITH9 protocol. Physical mailbox receipts and quota controls remain separate. |
| Snapshot coalescing | Comparing only a frame offset cannot preserve independent sender, receiver and credit frontiers when controls arrive out of order. | Merge cumulative progress fields monotonically at the engine queue, carrier queue and outgoing batch. Terminal-state compatibility remains checked. |
| Feedback carrier placement | Repeated bulk updates can scatter into the quiet paths intended for OPEN and small request/reply exchanges. | Stable per-stream feedback affinity uses available ordinary readers and replaces an already queued snapshot. Interactive initial credit, repairs and terminal controls retain their routing. Affinity is removed when the stream retires. |
| Target DNS and address policy | Go's IPv4 resolver can return an IPv4-mapped address rejected by a policy expecting native IPv4. | Normalize resolver results with Unmap before policy checking and numeric dialing. Mapped private and reserved addresses remain blocked in production. |
| Native bridge coverage | Earlier socket-based load tests did not cover the native TCP bridge with the IMAP transport. | A host-only fixture exercises native gonet TCP, encrypted IMAP supervisors, server exit, Single/eight-stream/consecutive traffic and verified payloads. Production builds exclude the fixture. |
| DATA redial backoff | The final suite caught additive jitter stretching the nominal 1+2+4 second recovery budget to 8.4 seconds, beyond the existing 8-second check. It could also stretch a 30-second backoff cap to 36 seconds. | Subtractive bounded jitter preserves desynchronization and the stated backoff ceiling. The recovery deadline and transient/permanent failure classification are unchanged. |
| Server management | Export and client changes required manual identifiers. | Numbered client/account selection displays identities and supports cancellation. Only enabled clients are offered for profile export. |
| Client retirement | Deleting files or reusing an identity could lose journal scope. | Removal retains a credential-free identity tombstone and the original journals; new registrations use new IDs and folders. No physical debt is refunded by removal. |
| Live traffic | Diagnostic traces are not a suitable realtime accounting interface. | Server-side exit sockets count successful payload bytes with atomics. A private atomic JSON snapshot is published once per second independently of diagnostics. The manager calculates rates from sample timestamps and detects resets/stale samples. |
| Server uninstallation | Removing configuration would also destroy keys and quota state. | Uninstallation checks the installed service identity, stops/disables it, and removes two installed files. It preserves server state and mailboxes. |

## Statistics semantics

Upload counts successful bytes written to the server's TCP exit. Download counts bytes read from that exit for transport to the client. These are useful TCP payload counters, not wire traffic, IMAP message sizes, retransmitted transport data or an assertion that the remote application consumed every byte. Partial I/O counts only successful bytes. Half-close keeps the socket active; full close decrements it once. Per-user counters are shared across accounts and reconnecting carriers for the lifetime of the server process.

The server publishes `tunnel-server/traffic.json` with private file permissions. Publication is outside the packet processing path and adds no IMAP requests. A snapshot older than five seconds is marked offline. An orderly shutdown publishes a final offline snapshot. Counters start afresh with a new server instance; the manager never calculates negative rates across that reset. Historical billing/accounting and durable traffic totals are not implemented.

## Validation limits

The baseline comparison retained from the transport investigation is mixed: the eight-second fragmented synthetic run measured Rev7 at 53.510 Mbps upload / 52.691 Mbps download, and the feedback candidate at 61.899 / 48.398 Mbps. Maximum echo latency was 421 ms and 330 ms respectively, with no failed probes. This single short A/B comparison does not demonstrate a general speed improvement. It predates the management additions. Failed intermediate evidence is kept separate from the final passing required gate.

The final bundle includes the complete required Go build report, focused race checks, native bridge tests and the latest executed load gate. Use those reports for exact counts and outcomes. Android APK compilation, Android domain tests and lint have not been executed for this development bundle; the source launcher enforces them during a local `all` build. Previous Rev7 Android passes do not validate Rev8.

Native host tests do not measure Android VPN/TUN packet reads, physical phone handovers, provider service times or live Ookla behavior. Full serial 200 Mbps qualification and device/provider tests remain pending. A lower control-command count alone cannot prove lower end-to-end RTT on every provider. Further work should measure per-phase useful payload, physical APPEND/FETCH counts, command service delay, loaded echo latency and actual Android CPU/TUN overhead together.

No backup-account reservation is added. Existing line-failure recovery remains in use. No throughput threshold, startup deadline, wire authentication check, physical quota limit or mailbox deletion proof is relaxed.
