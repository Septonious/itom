# Frozen transport: local application evidence

Transport tuning stops at this checkpoint. The next work is cold-start,
packaging and real-provider testing, not pursuing a higher local peak.

The independent provider varies client RTT through 10–50 ms every 500 ms and
server RTT through 20–40 ms every 700 ms. All client sockets and both directions
share 500 Mbps; all server sockets share 1000 Mbps. These are verified-TLS IMAP
and HTTPS application transfers, with receiver-side byte verification. Each
version keeps its own pinned client dependency; archived RC2 is unchanged.

| Case | RC2 upload/download | Rewrite upload/download | Rewrite maximum light RTT |
| --- | --- | --- | --- |
| Matched 12 s, independent latency | 172.67 / 144.54 Mbps | 280.17 / 312.85 Mbps | 214.02 ms |
| Frozen rewrite, 60 s each direction | Not rerun in this case | 291.42 / 317.20 Mbps | 270.71 ms |

The frozen 60-second run completed both HTTPS phases, all 30 client roles stayed
ready, and each physical account recorded exactly seven successful logins with
no refusal or delivery reconnection. Maximum loaded application RTT was
910.16 ms. The largest receiver gap after ramp was 201.40 ms upload / 418.83 ms
download. Every reported target passed, including rolling five-second 100 Mbps,
100 Mbps within two seconds and no 500 ms receiver pause during active sending.

These are local-model results, not Rambler, Android device or carrier results.
The model does not reproduce provider IOPS/throttling, radio loss or Android CPU
and JNI/TUN throughput. No field bandwidth or connection guarantee follows from
these numbers. See the qualification harness README for exact model limits.

## Changes selected by the investigation

- Control uses measured-service-time NOOP polling, avoiding the IDLE exit RTT.
  DATA keeps IDLE and missing-notification repair. Fresh EXISTS skips redundant
  STATUS.
- Only initial isolated small application/open/ping packets use two distinct
  ready accounts, with identical ciphertext and receiver deduplication. Bulk,
  credit and receipt traffic are not duplicated by this policy.
- Bulk fills a bounded 128 KiB account quantum, with no fill timer. Segments
  contain at most 32 bounded messages rather than eight. Tiny framing writes
  adjacent to bulk stay with DATA; isolated small writes still dispatch at once.
- The packet allowance is 2048 while the pending-byte limit stays 32 MiB. This
  removes an accidental limit caused by 512 small framing packets filling the
  slot budget before the byte budget. Receive and bulk-byte limits are unchanged.
- Established bulk advertises actual consumption in 256 KiB increments; the
  initial window expansion and final consumption remain immediate.
- Dispatch scans pending packets once per wake and sorts outside the session
  lock. Receipt batches survive local writer backpressure rather than being
  discarded and forcing a retransmission of already delivered application data.
- Diagnostics include session failure reason, receipt queue pressure and
  retransmissions. The harness retains server and client traces on failure.

## Failed attempts are retained

`comparisons.json` and `local-fixture-evidence.zip` retain all 25 local attempts,
including slower upload variants, a 60-second loaded ping timeout, a later
duplex timeout, and RTT/throughput target failures. The first RC2 failure was a
fixture local-exit permission mismatch, corrected using RC2's existing test-only
local-exit adapter with the real TLS mailbox dialer. It is not counted as a
broken RC2 transport. Early paired-clock and zero-added-latency cases are
calibration/history, not interchangeable with the final independent-clock case.

The initial short cases could pass while a longer case stalled. The current
frozen run is therefore recorded separately and does not erase those failures.
Root causes of earlier field failures cannot be inferred solely from this model.

## Safety verification

The final source gate passed normal and race builds of core, mailstream and
native, the pinned IMAP library suite, dependency/source checks and vet. The
mailstream application cohort verifies nine 20,123,456-byte HTTPS echoes across
fresh sessions with cleanup available, refused and stalled. Each scenario also
checks a complete 1 MiB response after request half-close. Native proof runs
actual SOCKS and IPv4 TUN paths, SIGKILL recovery, authenticated admission and
same-user replacement. Legacy accounting is preserved.

The old 4 MB fixture no longer filled a segment after batching improved; that
failed coverage check is retained. The larger race-instrumented object then
outgrew the old per-request safety deadline while making progress, with no race
report. Safety deadlines now accommodate the larger instrumented object. The
non-instrumented performance gates retain 300 ms / 2000 ms / 100 Mbps limits.

Old-session reclamation, append-only discovery logs and recovery after clearing
private app data remain production limitations. A bounded, explicitly experimental
test build is for verifying startup and real-provider behavior, not a declaration
of sustained production readiness.
