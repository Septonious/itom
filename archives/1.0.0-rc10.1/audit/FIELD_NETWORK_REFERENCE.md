# Network reference for upcoming RC10.1 field tests

User-provided Speedtest screenshot, recorded 2026-10-19. Measured through VLESS on port 443, Moscow to Sweden. The user plans to test RC10.1 on this access network. This reference describes the measured VLESS path; the screenshot does not establish direct client-to-Rambler IMAP timing or whether VLESS will remain active during ITOM tests.

| Metric | Measurement |
|---|---:|
| Download | 89.6 Mbps |
| Upload | 96.0 Mbps |
| Idle latency | 49 ms |
| Download-loaded latency | 3415 ms |
| Upload-loaded latency | 248 ms |
| Reported jitter | 15 ms |

The VLESS reference has a large latency increase under download saturation (3415 - 49 = 3366 ms), and a smaller increase under upload saturation (248 - 49 = 199 ms). This is consistent with substantial queueing or stalls somewhere along the measured end-to-end path. It does not identify whether the access network, VLESS implementation/endpoint, route, or Speedtest endpoint caused the increase. Reported jitter is a separate statistic and does not negate the loaded latency measurements.

For the RC10.1 analysis, retain idle and loaded latency separately, correlate spikes with both endpoints' stage profiles and outstanding work, and document the actual routing used during ITOM tests. A single VLESS measurement is not a hard bound on the access link or ITOM throughput and must not be used to waive qualification thresholds or claim a transport regression by itself. No direct client-to-Rambler measurement, repeatability, packet loss or concurrent traffic information is available in this note.

This is field context only. No simulation, source change, rebuild, qualification result or change to the published RC10.1 artifacts is implied.
