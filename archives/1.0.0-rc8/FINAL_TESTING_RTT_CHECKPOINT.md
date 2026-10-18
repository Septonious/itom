# RC8 late-stage RTT selection and test package
Date: 2026-10-18

This source change does **not** qualify RC8 as stable or waive any hard gate.
Observed paired no-stream account RTTs were 165ms and 583ms, both with 7ms IMAP
operation latency and 2/2 physical lanes. Last RTT is not a promise of equal
application RTT or proof of the mail provider's root cause.

- With **zero logical streams** and no local physical/control debt, use a fresh
  (<=10s) smoothed echo as the short-placement path cost, before a much older
  (<=120s) unloaded baseline. Idle lane changes are advisory; no credit, grant,
  journal, on-wire format, or TCP identity changes.
- While active, physically busy or stale, retain original unloaded baseline,
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
