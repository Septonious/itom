# RC8 late-stage RTT selection and test package
Date: 2026-10-18

This source change does **not** qualify RC8 as stable or waive any hard gate.
Observed paired no-stream account RTTs were 165ms and 583ms, both with 7ms IMAP
operation latency and 2/2 physical lanes. Last RTT is not a promise of equal
application RTT or proof of the mail provider's root cause.

- With **zero logical streams, zero outstanding logical bulk**, and no local physical/control debt, use a fresh
  (<=10s) smoothed echo as the short-placement path cost, before a much older
  (<=120s) unloaded baseline. Idle lane changes are advisory; no credit, grant,
  journal, on-wire format, or TCP identity changes.
- While active, while logical bulk debt remains, physically busy or stale, retain original unloaded baseline,
  pool fallback, account eligibility and bounded grace.
- The original descriptor-cache 512-entry headroom correction is retained.
- Regression `TestRC8FreshQuietEchoOverridesOlderIdleBaseline` checks idle,
  loaded, stale and physically occupied cases.
- The workflow `itom-rc8-testing-package.yml` assembles a matched debug APK,
  Linux amd64/arm64 executables, exact source and public hash/signer evidence.
  This is **testing**, not a release tag. It must only be distributed as
  unqualified while RC8 strict CI and real Android/Rambler validation remain red.

Accept RC8 only with >=75Mbps each direction for entire 60s, tail 30s and
tail 10s, 0 missing probes and maximum loaded RTT <=750ms, plus genuine
10/20Mbps startup continuity, fresh/retained recovery and real-device tests.

## Final test checkpoint (source commit 91db2a504a0615c6c3837ccb84001322c73c5f6f)

Matched RC8 APK, Linux amd64/arm64 and source packaging PASS:
https://github.com/Septonious/itom/actions/runs/39133679427

Focused core, Go vet, Android checks and 4-stream throughput PASS; strict
32-stream throughput FAIL because last 10-second download = 73.374106 Mbps
despite 60-second 132.029/78.237 Mbps and max RTT 401.053 ms:
https://github.com/Septonious/itom/actions/runs/39133679435

The preceding experimental commit had an unexpected download-progress
blackhole during 32-stream load. This remains a reliability investigation,
not a field-stability guarantee. Full original-app/source tests, repeated
fresh+retained qualification, low-link startup, real Android/Rambler test
and <=100ms jitter goal remain unqualified.

For complete Codex handoff, read CODEX_HANDOFF_2026-10-18.md.
