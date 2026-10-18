# ITOM 1.0.0 RC9 development

Based on RC8 checkpoint `7851e22`, copied without resetting journals or accounts.
Current experiment bounds simultaneous outgoing DATA proof-body reads to four
per logical session. The 4 MiB command bound and physical deletion proofs remain;
control discovery, remote cleanup, presence and deletion do not use these read
slots. Cancellation and read failure release slots. This is a scheduling change,
not a quota, journal, delivery-flight or repair-policy change.

Local checks passed: source identity, 92 Python tooling tests, focused RC8/RC9
cleanup, descriptor replay and independent-stream recovery regressions.
Performance benefit is unverified until repeated CI qualification completes.
RC8 evidence is retained under `archives/1.0.0-rc8/`; its three repeated failures
and early low-link failures remain blockers, not waived baselines.

`.github/workflows/itom-rc9-qualification.yml` runs fresh and retained SINGLE/4/32
streams twice for 60 seconds each. Each full-capacity direction must meet
75.000 Mbps for full, trailing 30s and trailing 10s windows, maximum loaded RTT
750 ms, complete probe delivery and original/new socket integrity. Genuine
10/20 Mbps startup ramps retain their latency and continuity gates. Recovery,
quota, proof and race checks must pass as well. APK and Linux build jobs depend
on all qualification jobs; no artifact is built if any gate fails.

Android package stays `org.itom.client`; versionCode 10008, revision
`itom-1-0-0-rc9`. APK signer evidence must be compared with the installed app
before an in-place upgrade. No signing-key replacement is authorized. Laboratory
qualification never certifies an Android/Rambler path or sub-200 ms field RTT.
