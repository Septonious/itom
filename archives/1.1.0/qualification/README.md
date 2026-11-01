# Application throughput comparison with archived RC2

This harness compiles the same HTTPS application worker against two separate
production cores: frozen `archives/1.0.0-rc2/source/core` and the current
mailbox-stream rewrite. Each keeps its own pinned IMAP client library. It does
not change RC2's transport, grant controller, cryptography, cleanup or timings.
The cores run in separate processes against a freshly restarted provider each
time. Source fingerprints and failures are retained. No live accounts, server
credentials or uploaded customer diagnostics are used.

The only version-specific adapter permits the local origin. RC2's existing
`itomtest` entry point enables its local-exit policy with the **default real TLS
mailbox dialer**. The rewrite's public managed-server hook permits only the two
actual loopback origin addresses. The application worker itself is identical.

The provider uses the IMAP SDK's memory server, shared by separate verified-TLS
client and server listeners. Their RTT schedules change independently:

| Endpoint | Repeating RTT schedule | Shared wire capacity |
| --- | --- | --- |
| Client–IMAP | 10, 25, 50, 35 ms, every 500 ms | 500 Mbps across all sockets and both directions |
| Server–IMAP | 40, 20, 30, 25 ms, every 700 ms | 1000 Mbps across all sockets and both directions |

A pipelined propagation/serialization queue operates underneath TLS, in 16 KiB
pieces with bounded per-socket queues. It does not sleep on each application
Write. Every phase resets the schedule's starting clock, without clearing queues
or forgiving wire bytes. Both versions encounter the same phase-relative
schedule. The provider enforces ten simultaneous connections per physical
account and records successful logins, peak connections, refusals, wire bytes,
command counts and APPEND body sizes.

The worker opens actual forwarded TCP streams and completes actual HTTPS
handshakes. Uploads and downloads verify a deterministic byte pattern at the
receiver. Upload completion also compares the receiver's count with the exact
producer count. A separate persistent TLS echo stream measures application RTT
before, during and after load. Concurrent HTTPS streams and simultaneous upload
and download use the same origin and verification.

Reported Mbps uses **verified application bytes divided by full phase time**,
including TLS, ramp and final drain. Half-second buckets preserve fluctuations.
Functional success is separate from target success: a passing transfer can still
fail the 300 ms light RTT ceiling, 2000 ms loaded ceiling, 100 Mbps mean, first
100 Mbps bucket within two seconds, five-second rolling 100 Mbps or absence of
half-second receiver stalls after ramp, including pauses spanning two buckets. A zero-rate bucket is not discarded.
Both orderings are supported to reduce first-run bias. No race-instrumented
throughput figure is used as a speed result.

Run with pinned Go 1.27.1 and an unused output directory:

```sh
python3 compare.py --go /path/to/go1.27.1/bin/go --out /tmp/itom-single --duration 12s
python3 compare.py --go /path/to/go1.27.1/bin/go --out /tmp/itom-multi --duration 12s --streams 4
python3 compare.py --go /path/to/go1.27.1/bin/go --out /tmp/itom-duplex --duration 20s --duplex --order candidate-first
python3 compare.py --go /path/to/go1.27.1/bin/go --out /tmp/itom-sustained --duration 60s
```

Use one performance case at a time on an otherwise idle host. Temporary keys
and state are private and removed by the runner. Result JSON, failed logs and
CPU profiles remain in the output directory. A timed-out process is reported
as a timeout, not a successful zero-speed measurement.

## Limits

This is a bounded application-level network model, not Rambler or an Android
device. It does not emulate radio loss, kernel congestion windows, carrier
handover, provider storage IOPS, message/mailbox quota enforcement, arbitrary
notification suppression or provider throttling. The shared host's CPU and
memory server differ from production. A zero-added-latency run is calibration
only; a transient 200+ Mbps bucket is not evidence of mobile sustained speed.

These comparisons can expose regressions and validate complete application
paths under the stated schedules. They cannot prove field parity or sustained production readiness. An explicitly
experimental test build also requires matched artifacts and startup checks. Old-session ownership/reclamation, discovery-log retirement,
reinstall recovery and device/provider qualification remain release blockers.
See `../verification/mailstream-v3/performance/` for retained measurements and
the conditions under which each was taken.
