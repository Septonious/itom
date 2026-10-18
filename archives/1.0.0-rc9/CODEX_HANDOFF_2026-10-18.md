# Continue ITOM RC9 development — BLOCKED, not release-ready

Repository: Septonious/itom. Branch: `codex/v1.0.0-rc9-development`.
Current runtime is restored exactly to `8868df3` after rejecting `0dc4e91`.
Final source checkpoint: `eef1123` (later documentation-only commits do not
change it). Repeat qualification completed and **failed**:
https://github.com/Septonious/itom/actions/runs/39146596629
**No RC9 APK or Linux binary was built.** Do not build or promote with failing
gates. The workflow blocked all artifact jobs as intended. The earlier 12/12
pass did not reproduce and must not be presented as release qualification.

## Retained changes

- `41688d1`: limit simultaneous DATA proof-body reads to four per logical
  group; preserve the 4MiB discovery command bound, control/remote/presence
  service, physical deletion proofs, journals and all quota authority.
- `8868df3`: correct unloaded ACK calibration to use half the actual initial
  window, and retain later bulk exposure until ACK. The regression fails with
  the prior calibration (8 loaded samples admitted), and passes with the fix.
  Both endpoints start at 256KiB; role ceilings remain 16MiB server/12MiB client.
- `8868df3`: pace only first-frame exceptions of unproven new streams, at
  most one per controller epoch. Proven independent streams retain their
  existing immediate escape; repairs and physical debt are unchanged.
- Reverted `0dc4e91` global exception restriction: retained 4-stream full
  rates fell below75Mbps, with a one-second upload stall. Extended recovery
  tests passed, but those cannot waive retained-throughput or latency gates.
- Reverted `f549009` short-reader audit experiment: retained 32-stream tails
  regressed to 34.157/47.789Mbps. Original IDLE/audit behavior is restored.

## Evidence and qualification

`8868df3` passed **12/12** strict full-capacity 60s rounds: SINGLE/4/32 streams,
fresh/retained state, two repeats. Mean of 360 logged whole-millisecond loaded
probe RTTs was approximately 295.81ms; maximum exact RTT was 546.543ms. Every
round met full/last30s/last10s >=75.000Mbps each direction, all 30 probes,
positive duplex in every 1s window, original TCP identities and post-load
original/fresh-socket integrity. Core/recovery/race checks passed.
Early 10Mbps still timed out; early 20Mbps exceeded 750ms. No APK/binary was
built because the complete qualification remained red.

`0dc4e91` completed qualification and is rejected. Full chronology is retained
in `evidence/0dc4e91-qualification.json`; do not reintroduce that scheduling
change merely because it stopped low-rate probe loss. Its targeted delivery/calibration and Rev4
independent-stream regression selection passed locally. A 2-core local early
10Mbps run reached ~55s before a missed probe cadence, with maximum RTT1114ms;
it is a FAIL, never a throughput or latency certification. CI has the unchanged
hard gates and additionally requires delayed BODY visibility/reader restart,
retained mixed traffic and >60s blackout recovery tests.

Filtered chronological load evidence for every earlier candidate, including
failed/outlier rounds, lives in `evidence/`. Source identity and 92 Python
packaging tests passed locally. Android SDK tests/lint, both JNI ABIs and signer
verification run in the gated build; they have not run for RC9 yet.

## Invariants and build identity

No accounts/journals/wallets reset. No physical quota or uncertainty rule
changed. Unknown APPEND remains charged; authenticated exact deletion proofs
alone reclaim physical debt. No timeout, RTT or throughput gate was relaxed.
No protocol journal schema or existing TCP/session identity was replaced.
Android stays `org.itom.client`, versionCode10008, versionName1.0.0-rc9,
revision`itom-1-0-0-rc9`. Existing package/signing requirements remain; compare
actual signer against the installed application before an in-place upgrade.
Real Android/Rambler validation is required before release qualification.

Source: `archives/1.0.0-rc9/source`. `.github/workflows/itom-rc9-qualification.yml`
contains reproducible strict cases and gated matching APK, Linux amd64/arm64,
source ZIP and checksum/signer artifact jobs. No release tag is published.

## Final repeat — current blockers

The unchanged restored source completed only **8/12** strict full-capacity cases:

| Case | Round1 | Round2 |
|---|---|---|
| SINGLE fresh | FAIL,850.441ms RTT | PASS |
| 4 fresh | FAIL,68.835Mbps down,1166.058ms RTT,1s download stall | FAIL,51.363Mbps minimum tail,1160.829ms RTT |
| 32 fresh | PASS | PASS |
| SINGLE retained | PASS | PASS |
| 4 retained | PASS | FAIL,zero download bytes in1s window51 |
| 32 retained | PASS | PASS |

All full-capacity cases still completed 60s,30/30 probes, original TCP identity
and post-load original/fresh-socket integrity. This does NOT waive their RTT,
positive-duplex-window or throughput failures. Early10Mbps timed out after6.5s
(max completed RTT1446ms); early20Mbps completed but maxRTT1136.082ms failed.
Expanded core, quota/proof, race, delayed-BODY/reader restart, retained mixed
traffic and >60s blackout recovery tests all passed on this restored source.
APK/Android/lint/JNI/Linux/source-package build jobs correctly skipped.
Full chronology is `evidence/eef1123-repeat-qualification.json`.

The retained-source calibration regression is a proven logic correction, not
proof that mean RTT or field Speedtest reliability improved. Do not select only
the earlier all-green matrix. All five rounds of candidate evidence remain.
Next investigation should target 4-stream interactive APPEND/FETCH queue dwell
and the one-second download holes, with low-overhead segment timing retained
until after measurement. Compare actual service/cleanup and physical occupancy
before changing admission again. Avoid broad new flight reductions: both audit
and global exception restrictions already regressed retained throughput.
