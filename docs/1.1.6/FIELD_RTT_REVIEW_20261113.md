# ITOM field RTT review — 2026-11-13 (provisional)

## Evidence and limits

The new user screenshots show an aggregate RTT reading of 146 ms, per-account
last acknowledged-delivery RTT values of 196/203/432 ms, and one stale sample.
They also show three active lanes per account, while the published 1.1.5 and
1.1.6 RC1 sources expose four physical connection roles per account. This is
an **unresolved build/version mismatch**. Inspect the Android diagnostics
archive's build field and the server archive's capture revision before
deploying code. Screenshots show zero instantaneous tunnel throughput, so the
231/454 Mbps speed-test screenshot cannot by itself establish loaded tunnel
RTT or even that the test traversed the tunnel.

Historical repository release notes for 1.1.5 report **290/340 Mbps peaks at
approximately 200 ms loaded RTT**. Those are earlier user field observations,
not matched A/B trials against the current screenshots. The 1.1.6 RC1 notes
report that latency-aware account scoring was explicitly disabled after
regressing home/mobile loaded p95 in paired tests. Do not re-enable it on
the strength of one low screenshot RTT.

The two ZIPs supplied in the chat are **not analyzed by this repository
change**. The offline analyzer must be run on those ZIPs before any causal
diagnosis or transport change is claimed.

## Confirmed semantics from the released source

- mailstream/storage.go releaseDelivery updates account RTT on a qualifying
  authenticated delivery acknowledgement, only if the delivery was not
  retried. The last RTT is not an active per-account echo probe.
- mailstream/imap.go Snapshot marks RTT stale after 30 seconds without such
  a sample. This does not imply the account's sockets are broken.
- The IMAP latency field is sampled on the control pipe and is not an
  end-to-end encrypted application round trip.
- core/tunnel/api.go samplePing runs on a two-second cadence and retains the
  last successful RTT after a failed probe; the UI's aggregate RTT lacks an
  explicit freshness marker.
- core/tunnel/api.go counts active connection roles, not their per-request
  response health.
- The 1.1.6 RC1 release note documents four connections/account and the
  disabled experimental account scoring. Avoid blindly changing the
  scheduler, writer counts or retry timeouts.

## How to produce a redacted comparison

Requires only Python 3.10+; no dependencies, server connection or app install.

Run from the repository root, with the ZIPs downloaded to the same machine:

    python tools/itom_field_trace_compare.py \
      --client "ITOM_diagnostics_full(20261113-014006).zip" \
      --server "ITOM_server_full_trace(1).zip" \
      --output itom_field_summary.json

For an older matched field capture, append:

    --baseline-client "older_android_diagnostics.zip" \
    --baseline-server "older_server_diagnostics.zip"

On Windows PowerShell, use a single command line or backtick line
continuations instead of shell backslashes.

Run synthetic checks:

    python -m unittest discover -s tools -p "test_itom_field_trace_compare.py"

Only the **sanitized JSON summary** should be shared for review. The raw
archives may still contain account identifiers, metadata, or diagnostic
context; keep them private. The script never uploads data. Account aliases
are hashes, not a cryptographic anonymity guarantee.

The summary provides per-account fresh/stale observation counts, longest
observed stale span, snapshot-weighted fresh RTT p50/p95/p99, IMAP command
latency, append completion, queue bytes, observed recovery/backpressure,
writer reconnects, and matching-baseline deltas. It records the build and
revision metadata needed to resolve the 3/3-versus-4/4 discrepancy.

## Proposed field experiment before changing transport behavior

1. Capture 5–10 minutes with a connected, quiet tunnel.
2. Capture 10 minutes of sustained YouTube with at least one additional
   short-request workload. Record which app's traffic uses ITOM.
3. Capture 5–10 minutes after the video stops, without restarting the
   tunnel. Record time of stop/start separately.
4. Export Android and server archives promptly. Preserve the exact build,
   server binary and configuration.
5. Compare account fresh/stale fractions and queue/append/reconnect metrics
   by phase, then determine whether stale RTT means simply no traffic,
   transport congestion, stalled publication, failed writer, missing
   notifications, or a probe display artifact.
6. Repeat a matched A/B sequence before changing any scheduling or recovery
   threshold. Track idle p50/p95, loaded p50/p95/p99, short-request TTFB,
   useful download/upload, provider command count, CPU and account fairness.

Do not equate a single minimum RTT with the sustained idle p95. Do not
recycle an account solely because its last delivery RTT is stale; doing so
could increase login pressure, mailbox debt and reconnection risk.

## Candidate next patch (not implemented here)

After evidence identifies the bottleneck, add non-invasive diagnostic fields
for: account RTT sample age and provenance, global ping success age and
failure streak, control reader FETCH age, per-account pending APPEND queue
age, and authenticated progress age. Only then consider bounded, hysteretic
account rehabilitation based on actual failure and progress, not a static
RTT threshold. Preserve all durable quota/receipt/replay/cleanup invariants.

This branch adds only the offline analyzer and synthetic tests. It changes
no protocol, runtime transport, Android package or server binary.
