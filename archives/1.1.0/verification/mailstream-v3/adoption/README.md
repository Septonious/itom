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

`domain-jvm.log` and its summary record all 93 actual domain tests passing on
an isolated JVM build (JDK 21, bytecode target 17); this does not compile the
Android app or replace device testing. The GitHub application workflow passed
on runtime commit `1355987382fd96b3a3876d12a3cfdd3c6bfd6cbb`:
https://github.com/Septonious/itom/actions/runs/41246581669

## Remaining structural blockers

Physical debt is still process-local and discovery logs do not yet retire.
Successful six-session reuse is not durable process/reinstall qualification.
The retirement queue has its own worker but still borrows the control receiver's
socket. Tagged DELETE refusal is isolated; a hanging DELETE can delay that
socket's control receive path. Maintenance therefore needs independent carrier
ownership with explicit physical connection admission before a testing RC.
The current cohort injects delayed APPEND completion and DELETE refusal; it
does not establish isolation from an arbitrarily stalled DELETE. These limits
must not be masked by a longer application deadline or a larger storage budget.
