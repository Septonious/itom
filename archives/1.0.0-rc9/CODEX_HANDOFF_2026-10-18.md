# Continue ITOM RC9 development

Repository: Septonious/itom. Branch: `codex/v1.0.0-rc9-development`.
Current runtime candidate: `0dc4e91`; qualification run:
https://github.com/Septonious/itom/actions/runs/39146157950
**Do not build or promote a release with failing gates.** The workflow blocks
APK/Linux builds behind all core, repeated-load and low-rate startup jobs.

## Retained changes

- `41688d1`: limit simultaneous DATA proof-body reads to four per logical
  group; preserve the 4MiB discovery command bound, control/remote/presence
  service, physical deletion proofs, journals and all quota authority.
- `8868df3`: correct unloaded ACK calibration to use half the actual initial
  window, and retain later bulk exposure until ACK. The regression fails with
  the prior calibration (8 loaded samples admitted), and passes with the fix.
  Both endpoints start at 256KiB; role ceilings remain 16MiB server/12MiB client.
- `0dc4e91`: normally at most two complete records may be independent-stream
  exceptions above the shared window. An ACK releases charged logical occupancy
  immediately. If both BODYs remain invisible, one additional fully charged
  frame per second can discover another stream. Existing actor wakes implement
  the pacing; no speculative APPEND replay, credit return or socket replacement.
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

`0dc4e91` is pending its own full qualification; the prior pass cannot qualify
this subsequent scheduling change. Its targeted delivery/calibration and Rev4
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
