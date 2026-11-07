# ITOM 1.1.3 mobile feedback qualification

The new `native-tun` frontend runs the actual native client and gVisor TCP/TUN
path. Its separate application stack exchanges IPv4 packets over a nonblocking
Unix datagram pair. Both fixture descriptors request 4 MiB socket buffers to
avoid injecting packet-send errors; physical provider wire shaping is unchanged.
Actual send/receive socket buffer sizes are recorded in `QualificationTUNBuffers`.
`QualificationAppTCP` belongs to the application fixture; the `Native` TCP
counters belong to the production native stack. Do not conflate them. The
reference receives the same observation-only native diagnostic fields, with
its published controller and all protocol/cleanup code unchanged.

The optional mobile-tail profile models a changing client-to-IMAP path with
30, 50, 180, 70, 35, 250, 80 and 45 ms delays. This is a generated hypothesis,
not a replay of captured packets. The native comparison shapes download/upload
at 150/90 Mbps, adds full-body and APPEND service time, and compares two timed
download/upload cycles. Base64 MIME overhead consumes part of that wire budget.

`--timed-speedtest --cancel-download-after 16s --download-first` records requests
cancelled while awaiting headers as well as partial responses. Every delivered
byte is checked, aggregate progress is required, and upload bodies must finish
with exact byte-count agreement. Aborted/completed request counts stay in the
result. Per-phase loaded-ping samples are retained, so a stalled reference phase does
not make an overall latency percentile look deceptively favorable. The
pre-existing strict cancellation mode is unchanged. Independent
`--upload-streams 6` exercises fewer productive streams alongside the stress
case with 43 streams; neither is claimed to reproduce the phone exactly.

`check_radio_native.py` preserves all rates and latency comparisons, rejects
fixture send errors, and enforces the existing two-second loaded-RTT ceiling.
It explicitly sets `publication_approved` and `field_performance_qualified` false.
A successful job is not approval to publish or evidence of phone throughput.

Existing qualification cases follow below for continuity.

# Application throughput comparison with archived RC2

This harness compiles the same HTTPS application worker against two separate
production cores: frozen `archives/1.0.0-rc2/source/core` and the current
mailbox-stream rewrite. Each keeps its own pinned IMAP client library. It does
not change RC2's transport, grant controller, cryptography, cleanup or timings.
The cores run in separate processes against a freshly restarted provider each
time. Source fingerprints and failures are retained. No live accounts, server
credentials or raw uploaded customer diagnostics are used. The physical recovery
fixture uses only an anonymous aggregate per-account debt vector from field review.

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

## Matched-server native startup and DNS checks

`startup/main.go` tests an actual downloaded server executable against the
production native Go entry point, exported profiles, 10/15-account defaults,
authenticated admin RPC, client restarts and server restarts. It is a platform
fixture, not an Android device test. The provider descriptor is generated by
`provider`; fixture state/profile exports must stay private.

`check_native_dns.py --go /path/to/go1.27.1/bin/go --out /tmp/native-dns` uses a
test-only overlay on the existing native application proof. It sends DNS over
the emulated TUN, races local resolvers on UDP/53, forces standards-based TCP/53
fallback, and verifies named HTTPS via real native SOCKS/TUN before and after
SIGKILL recovery. No production transport or exit policy is replaced; the
existing fixture maps its public test destination to a local TLS origin. Binding
the loopback fixtures to port 53 requires that local permission. Each repeat
uses a separate test process because Go caches trusted system roots per process
and each fixture generates a fresh private CA. TLS verification stays enabled.


For 1.1.2, `recovery_regressions.py` first reproduces five faults against the
published 1.1.1 source and then runs those exact tests against the candidate.
`check_physical_recovery.py` verifies three 20-second, 16-stream download/upload
cycles with 447,052,294 actual generated old DATA bytes. It permits one initial
upload of at least 90 Mbps during cold recovery, as explicitly accepted by the
user after a 96/146 Mbps replay; the other seven phases must exceed 120 Mbps.
All ownership proofs and physical limits remain required. No 150 Mbps guarantee
is inferred from a peak.

The optional `--client-rate-dip --client-mbps 100 --mobile-latency` case imposes
a shared 100/1/100 Mbps link, with a 15-second low-rate interval beginning ten
seconds after provider startup. Its absolute bandwidth clock is independent of
phase-relative RTT resets. Serialization integrates service across both dip
boundaries, so traffic queued earlier cannot bypass the low rate. Client IMAP
path RTT cycles through 30, 55, 100 and 70 ms. A 30-second application echo
observation permits the transient serialization backlog to drain; normal models
retain their three-second echo deadline. Record every loaded latency spike.
Subsequent complete transfers must average at least 30 Mbps on the restored
100 Mbps link. This stability model is distinct from the ordinary 120 Mbps
throughput gate on the 500 Mbps link.

`TestServerCleansOwnedMailAfterAbruptClientPowerLoss` cuts all phone sockets
without graceful close or final acknowledgement. The surviving server must empty
owned partial DATA and control mail, preserve unrelated mail, and perform every
STORE/EXPUNGE itself. The phone's mutation count must stay zero. Its two-second
test expiry accelerates the production server's unchanged 30-second idle expiry;
no transport timer or congestion setting is altered for qualification.

`lifecycle_regressions.py` reproduces the old service hook clearing a 67 MB
retention charge without any provider deletion, including a failed purge.
The candidate preserves the charge across first start, stop, restart, update
and failed purge; the lifecycle hook performs read-only verification. A service
restart is not a physical cleanup proof.
