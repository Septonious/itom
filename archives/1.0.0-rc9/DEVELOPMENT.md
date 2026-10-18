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

## First qualification and follow-up

Candidate `41688d1`, run 39144027093: all core/recovery and race checks passed.
9/12 repeated full-capacity rounds passed. Failures: SINGLE/fresh RTT 1071.617ms,
4/fresh RTT 1070.428ms, SINGLE/retained round 2 had zero download bytes in its
last one-second window despite 76.703Mbps trailing-10s rate. Retained 32-stream
rounds both passed (79.770/79.744Mbps minimum full/tail, max 418.443/466.382ms).
Early 10Mbps timed out; early 20Mbps max RTT was 1224.427ms. All full-rate rounds
completed 30/30 probes, original TCP integrity and post-load fresh socket checks.
Build jobs correctly skipped. Evidence is in `evidence/41688d1-qualification.json`.

The next change limits the notification-audit interval to 150ms on at most two
short readers. Shared mail uses its existing quiet reserve. Split mail has no
quiet reserve, so it remembers the two most recent actual short DATA carriers
for at most five seconds. Empty polls, OPEN and ordinary bulk/control traffic
cannot extend that hint; completely idle accounts retain the old cadence.
There is no global poll-rate increase, flight contraction, new timeout, credit
return or socket replacement. Its measured benefit is still to be established.
