# RC10.1 home capture — RC11 input

The client used a home 500 Mbps connection; VLESS was off. Cyan/purple is MULTI. Reported successful run: 17.9/3.88 Mbps, idle/download-loaded/upload-loaded RTT 365/455/910 ms, jitter 104 ms; other attempts failed immediately. Account UI displayed 9–22 ms IMAP versus 201–350 ms tunnel RTT. These are distinct measurements, not an additive pipeline budget.

Retained traces: client 18:37:37–18:39:56 Moscow, server 18:37:47–18:40:34 Moscow. Earlier events were removed by rotation. Both endpoints were RC10.1. All 65 retained server target dials succeeded. At 18:37:50 many client application sockets reset together; this is consistent with application cancellation but does not identify its trigger. No retained target-dial failure explains the failed tests.

Client control APPENDs 5,814 versus 1,231 DATA APPENDs; 544 historical reconciliation deferrals reported WALLET_HISTORY_MISSING. Counts describe operations, not bandwidth. Historical debt must remain charged.

Across per-account first/last health snapshots, server interactive account selection averaged 104.805 ms (p95 histogram upper bound 512 ms), journal preparation 48.577 ms (192 ms), completion 42.448 ms (192 ms), physical batch 164.402 ms (384 ms). These populations differ and must not be added as RTT percentiles. Client interactive physical batch averaged 44.648 ms. Retained server selection events separately had p95 378.408 ms and maximum 1,395.038 ms.

This supports reducing repeated durable work and recovery/control contention before shrinking throughput windows. It does not prove faulty server hardware or establish the exact cause of every Speedtest failure. Server recovery after the client trace ends may reflect shutdown/export; it is not proof of a load-induced carrier failure. Raw captures remain private.
