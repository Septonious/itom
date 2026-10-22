# DEV4 real-world feedback and the qualification gap

Six completed screenshots show these averages; earlier failed attempts are additional failures and are not included in screenshot means.

| Mode | Download Mbps | Upload Mbps | Baseline RTT ms | Download RTT ms | Upload RTT ms |
| --- | ---: | ---: | ---: | ---: | ---: |
| MULTI (3) | 66.7 | 33.4 | 356 | 495 | 996 |
| SINGLE (3) | 13.1 | 68.0 | 353 | 439 | 838 |

The uploaded client trace retains 95 seconds of the end of the session. At capture all15 accounts/30 directions are ready and no carrier reconnects are recorded. It cannot reconstruct the lost early startup/ramp failures. Application RESET/CLOSED records near a phase boundary do not prove a transport-induced reset. All recorded completed IMAP operations succeed, but operation and queue durations are large: control APPEND mean89/p95 203/max748ms, DATA APPEND126/347/1527ms, FETCH35/140/1885ms, and STATUS enqueue wait77/249/649ms.

A representative DATA APPEND has only the control and receive sockets. Across captured socket snapshots, all physical command snapshots have two sockets; several accounts have a DATA-specific receive role. This differs from the original five-socket client lab topology. Live configuration values and the server diagnostic were not supplied, so a particular resource configuration cannot be asserted. Reinstallation was confirmed. 810 reconciliation requests were deferred for missing wallet history in the retained window, with353 WALLET_EXHAUSTED DATA completions. These are scheduling/permission events, not evidence of packet loss or an IMAP server outage.

The earlier200+Mbps figures were application-byte rates in long simulated TLS/IMAP transfers. The standard provider fixture has2ms processing latency. It did not establish Rambler throughput, cold first-ready-account startup, or a short sequential Speedtest result. The reported DEV4 download loss was the paired retained simultaneous-duplex measurement (8.4–13.0%), not a bound on real-world regression. The user's observed larger regression is not contradicted by that number. Treating the old pass count as sufficient release evidence was inadequate.

DEV5 addresses four concrete mechanisms: per-account/fair old-grant audit pacing, current-session grants before historical audits, faster useful-ACK-clocked bulk startup, and independent hot-reader search on shared-writer routes. It also gives the shared control socket one fair command turn and classifies fallback bulk as DATA rather than urgent. Queue timing and protocol priority change; quota limits, old charged reservations, proofs, wire frames, wallet history, delivery acknowledgement authority and connection caps do not.

Qualification now includes exact RC2 and DEV4 controls, three short download→upload runs with no warmup after the first ready account, single and16 streams, four and ten configured account sessions, optional50ms APPEND/10ms FETCH/25ms STORE+EXPUNGE processing sensitivity,500ms useful-byte windows, original OPEN/probe deadlines, and separate idle/light measurements. These provider-processing settings are sensitivity tests, not a calibrated replay or artificial server failure. The original60-second/20-round qualification and all thresholds remain unchanged. Neither passing these tests nor higher synthetic throughput certifies field performance.
