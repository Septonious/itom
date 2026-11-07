# ITOM FIELD62 — redesign candidate with speedtest-like load qualification

Revision `transport-s1-perf7-field62`. Android `0.9.0-dev2-field62-debug`, versionCode `9062`. Full user-facing archive `ITOM_TRANSPORT_FIELD62_SOURCE.zip`.

## Design and reason

The last FIELD61 client/server traces recorded ~202.5 MB versus ~18.5 MB of encoded bulk DATA in *different duration* trace windows. The server's delivery window remained at 768 KiB with observed original DATA age p50 4.65s and p95 5.98s, even while short ACK age p50 was ~384 ms. A small original delivery backlog can monopolize age feedback and force continual congestion-window reduction without a corresponding interactive-latency regression. Neither fast/bulk message counts nor synthetic 4-stream rates prove actual TCP speed.

**Two design changes are coupled with a new gate, not ad hoc throughput tuning:**

1. Physical DATA grant/slot placement returns to the earlier FIELD57 scheduler. It does not withhold a remote grant merely because a particular physical slot is momentarily occupied and does not retain the unqualified spare-capacity borrow policy from FIELD61. Grant provenance, quota and journal accounting remain unchanged.
2. `bulkRecovery` is an independent small 128KiB temporary credit *window* experiment. It requires fresh short-DATA ACK evidence (<=650ms), current bulk demand, in-flight bulk and observed useful progress. It lasts three seconds, compares received useful ACK rate against the previous rate, and rolls back on absent improvement or short-ACK regression over 100ms. Failed trials are locked out for 15s; accepted changes receive a five-second stability hold. Crucially it **does not release outstanding bytes**, adjust remote grants, or ignore short-data pressure. All physical slot limits and fast reserves remain fixed.
3. The `TestSpeedtestMultiflowAcceptance` production-path fixture runs 24 TCP uploads and 24 independent downloads at the same time, up to 32+32, via 10 real in-process IMAP/TLS accounts, actual durable sessions, 1-sec useful-byte measurements and concurrent echo probes. TCP OPEN is parallel with 8-way admission. It rejects either-direction failure and a slow final 10-second tail, even when aggregate speed looks acceptable.
4. Test profiles cover normal 130Mbps, 100→50→25→10Mbps provider shaping, single-account outage and fluctuating 10/25Mbps. Degraded profiles explicitly do not demand 40Mbps over a smaller input link.
5. A read-only trace analyzer exports IMAP batch classes and latency/window distributions, without interpreting MIME bytes as TCP goodput.

## Gate and Windows build

Run the source archive's normal command first:

```powershell
python .\ITOM_TRANSPORT_FIELD62_SOURCE.zip all --expect-revision transport-s1-perf7-field62 --accept-android-licenses
```

Then in the extracted `itom` directory with Go 1.27.1:

```powershell
python tools/run_speedtest_gate.py --profile normal --streams 24 --seconds 30 --log-dir reports/speedtest-field62
python tools/run_speedtest_gate.py --all --streams 24 --seconds 30 --log-dir reports/speedtest-all
```

The complete `all` gate **does not substitute for running this explicit realistic performance gate**. A release may be installed only if the full build, normal stress gate, and Android/VPS version check succeed. Then run three comparable real Speedtests, record 40/40 Mbps useful sustained throughput, idle/loaded RTT and jitter, and repeat a 20/20 line startup. Roll back both client/server as a pair on a timeout, download collapse or latency regression.

## Qualification caveat

The source build was created on a machine with Go 1.23.2 and no network access to obtain Go 1.27.1. The isolated `bulkRecovery` logic and offline Python/tooling tests were run; **the full compiled tunnel and 24+24 fixture, APK, provider field and 40/40Mbps qualification could NOT be run in this environment**. Do not label this field-stable or deploy without the steps above. Historical source and measured FIELD57 are preserved.
