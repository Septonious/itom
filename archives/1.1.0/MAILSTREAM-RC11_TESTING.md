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
