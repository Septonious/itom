# ITOM 1.1.0 RC1.4 — startup diagnostics

This diagnostic build fixes missing evidence during immediate connection
failure. It does **not** claim to fix the field connection failure, meet the
latency or bandwidth targets, or improve throughput.

## Install and capture

Update the APK in place. It uses the same application identity and persistent
signer as RC1.3. Keep the profile, app data and server accounting state.
The RC1.3 server is compatible: this build changes no wire format, mailbox
namespace, configuration binding or accounting authority. A server update is
not required to capture the client failure.

Attempt one connection, then export full diagnostics from Settings. The archive
now includes `startup-trace.jsonl` even if no runtime client was created. Minimal
startup evidence records only fixed stages/reasons, a configuration hash,
counts and timing, independently of optional detailed tracing. Enable tracing
before the attempt to add per-account connection stages to
`transport-trace.jsonl`. Neither trace contains credentials, provider reply
text, traffic bodies or target addresses.

## What changed

- The rewritten client had accepted the tracing option without creating a trace
  writer. Its constructor could fail with no exportable trace at all. Startup
  capture now begins before provider connections; selected account stages are
  also recorded when detailed tracing is enabled.
- Minimal startup traces rotate at 256 KiB into at most three generations;
  detailed account traces retain their existing 2 MiB/three-generation bound.
  The exporter includes both sets and reports their counts separately.
- Native startup flushes the final report before returning, after joining its
  heartbeat. An attempt shorter than one second no longer retains only the
  initial `PROFILE` report.
- Offer discovery, opening the saved wallet, publishing the request and waiting
  for an invitation report distinct phases. The final evidence includes a
  fixed failure-reason code. The UI no longer blames an old/stopped server for
  every discovery failure.

## Evidence and limits

The reported RC1.3 client archive contains zero trace files and only the initial
`PROFILE`/zero-selected report, despite returning `V2_STARTUP_RENDEZVOUS`.
The paired pasted server trace reaches `waiting_request` on RC1.3. These inputs
do not identify the failing phone operation or prove provider keyword refusal.

Regressions cover immediate constructor failure with tracing both off and on,
safe fixed-cause capture, export of a startup-only archive, and a real TLS IMAP
provider fixture refusing the authenticated request after accepting LOGIN and
SELECT. The refusal case must preserve its actual selected count and leave
only the original accounted offer, without publishing an invitation or an
uncharged client HELLO. Normal HTTPS startup/restart remains covered.

No real Android device or Rambler account was available in the build environment.
The next transport change requires the new trace from the actual failing
attempt; this release makes that evidence obtainable without another reinstall.
