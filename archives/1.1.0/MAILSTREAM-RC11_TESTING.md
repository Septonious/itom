# ITOM 1.1.0 MAILSTREAM-RC11 — upload testing candidate

[RC10 remains the first field-ready candidate](https://github.com/Septonious/itom/releases/tag/v1.1.0-mailstream-rc10).
RC11 is an experimental successor for upload testing; it has not received that
field designation. The real-world 100 Mbps upload target remains unverified.

Only client outbound DATA receives additional measured-rate headroom when its
flight is actually limiting. Initial flight stays 512 KiB; the headroom horizon
is capped at 1.25 seconds. Packet send/delivery timestamps protect capacity
estimates against compressed receipts. Server upload readers FETCH up to 32
already-available containers instead of eight, streaming bounded bodies without
waiting for a full batch. Client download readers and server download control
retain RC10 behavior. Diagnostics add smoothed bulk receipt RTT.

The mailbox roles, 128-container generation, exact audited ownership/refunds,
cleanup retry policy and conservative capacity accounting are preserved. RC10
field logs contain cleanup timeout/retry events; this release does not claim to
resolve those. DNS remains the same raced UDP/TCP resolvers, 250 ms timeout and
optional session-latched IMAP fallback. No automatic folder DELETE is introduced.

Install both the APK and matching server for the upload changes. Install the APK
in place, retaining app data and profile. Version code is 10136; the existing app
ID and signer are preserved. Upload the matching `.bin` to `/tmp` and use manager
option 18, or execute it directly:

```sh
chmod +x /tmp/ITOM-1.1.0-MAILSTREAM-RC11-linux-amd64.bin
sudo /tmp/ITOM-1.1.0-MAILSTREAM-RC11-linux-amd64.bin install
```

No manual mailbox cleaning is requested. Keep RC10 available for comparison.
Test a cold MULTI run, SINGLE, a second MULTI and manual VPN reconnect with
logging enabled before connection. Compare upload, idle/loaded RTT, download,
line stability and retention/reclamation diagnostics.

The new model adds a 350 ms server DATA-body FETCH delay while keeping NOOP,
control and client download reads independent. That delay is a stress hypothesis,
not a measured provider constant. Frozen RC10 completes short uploads at about
6.2 Mbps; an independent FIFO model also reproduces a half-second-receipt cap.
An initial faster prototype failed the weak-link gate and was rejected. The
compression-safe local candidate completes two 30-second uploads at 108.5/100.4
Mbps and downloads at 299.0/336.1 Mbps. Its 7 Mbps model completes at 4.72/4.39
Mbps with 101 ms idle/post RTT and 1709 ms loaded RTT, including cancellation and
fresh HTTPS recovery. These are prototype synthetic results. Final exact-source
checks and all strict target misses are recorded before publication.

## Final exact-source verification

Source `e3c61864048be2e9a7e4b328b5324a2d91f88d4b` passes the [matched build and acceptance gates](https://github.com/Septonious/itom/actions/runs/42037172443) and [independent source checks](https://github.com/Septonious/itom/actions/runs/42037172328). Normal/race application, native and SDK tests, the IMAP fork checks and vet pass. Android passes 93 domain tests, 8 app tests and lint with zero errors. Both JNI libraries, server binaries and source archive are verified. The persistent Android signer is unchanged.

Six actual-server startup/reconnect cycles pass: three at 30/30 roles and three at 45/45, including server restarts and verified manual-stop local-capacity resets. The RC9 idle feedback and the RC10 slow-receipt upload limit are reproduced against frozen source; pressure clearing, delayed-update ordering, asymmetric full ledgers, active historical deferral and automatic idle cleanup pass.

The following are controlled TLS IMAP/HTTPS models, not live-provider results.

| Model / streams / cycle | Upload Mbps | Download Mbps | Max idle/post ms | Max loaded ms |
| --- | ---: | ---: | ---: | ---: |
| ordinary / 1 / 1 | 276.5 | 325.3 | 131.6 | 511.9 |
| ordinary / 4 / 1 | 244.4 | 256.2 | 109.4 | 918.4 |
| ordinary / 4 / 2 | 239.5 | 313.5 | 109.4 | 918.4 |
| ordinary / 32 / 1 | 217.0 | 314.4 | 97.0 | 501.9 |
| retained control / 4 / 1 | 261.5 | 304.7 | 97.7 | 689.4 |
| retained control / 4 / 2 | 218.7 | 266.0 | 97.7 | 689.4 |
| 40 old folders + metadata cost / 4 / 1 | 245.3 | 190.0 | 183.9 | 974.3 |
| 40 old folders + metadata cost / 4 / 2 | 179.9 | 258.1 | 183.9 | 974.3 |
| 40 old folders + metadata cost / 4 / 3 | 108.7 | 191.4 | 183.9 | 974.3 |
| 350 ms upload FETCH / 4 / 1 | 101.5 | 325.7 | 103.1 | 402.5 |
| 350 ms upload FETCH / 4 / 2 | 107.5 | 328.9 | 103.1 | 402.5 |

The 7 Mbps shared-link cancellation/recovery model completes at 4.76/4.38 Mbps upload/download, with maximum idle/post RTT 104.9 ms and loaded RTT 1455.0 ms. No account fails or is rejected. RC10 remains the first field-ready release; RC11 requires a new field upload test.

The account-interference gate completes three full speedtests above 100 Mbps phase averages, with no active historical LIST. Full-phase capable-path and idle/loaded RTT gates pass. Strict pause/rolling-throughput misses remain in the raw evidence and metadata; all sustained-window and live-provider goals are not claimed complete.
