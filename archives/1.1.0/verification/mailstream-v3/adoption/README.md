# Replacement runtime adoption evidence

These are local application-integrity checks, not provider performance results.
All accounts, keys and HTTPS endpoints in these scenarios are local fixtures.

- `before-select-publication-fix.jsonl`: retained intermittent failure after the
  startup-order change. A selected DATA receiver saw no published mailbox state;
  one account failed and HTTPS delivery later timed out.
- `application.jsonl`: corrected pinned-library completion ordering; six HTTPS
  sessions pass across user VPN restarts with deletion both available and refused.
  Each session uploads and downloads 4,325,376 bytes. Both modes also preserve a
  complete 1 MiB TCP response after request half-close.
- `core-admission.jsonl`: actual server/native SOCKS and IPv4 TUN application
  paths; two configured identities; second identity receives authenticated BUSY;
  first identity restarts and useful server-exit counters are checked. Observed
  provider peak remains within ten sockets, including after the SELECT fix.
- `native.jsonl`: native startup cancellation, direct DNS policy and application
  copy checks. This run preceded the SELECT ordering fix.
- `imap-library.log`: full pinned IMAP library race-instrumented suite after fix.

The GitHub workflow independently repeats the actual source modules on its
recorded commit. Android compilation/device checks, durable restart retention,
control-log retirement and mobile/provider throughput qualification remain open.
No testing RC or APK is published by this checkpoint.
