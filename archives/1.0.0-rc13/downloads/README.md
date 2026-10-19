# RC13 server update — existing RC12 Android supported

**Keep the installed RC12 app and existing profile. Update the server first.** Reinstallation/data clearing deletes client wallet history and leaves old grants charged. The RC13 transport correction is server-side and uses the existing RC12 reduced-grant codec.

- [Complete APK + server binaries + source package](https://github.com/Septonious/itom/actions/runs/39335976534/artifacts/12226044474)
- [APK for fresh installations or a compatible signer](https://github.com/Septonious/itom/actions/runs/39335976534/artifacts/12227081643)
- [Linux amd64/arm64 binaries](https://github.com/Septonious/itom/actions/runs/39335976534/artifacts/12226424241)
- [Complete buildable source](https://github.com/Septonious/itom/actions/runs/39335976534/artifacts/12227011017)
- [Build run](https://github.com/Septonious/itom/actions/runs/39335976534)

All five CI jobs passed. Android executed82 domain and7 app tests with zero failures; lint has zero errors and one dependency-version advisory. CI verified package identity, both native revision markers, APK signature and assembled-package checksums. The APK uses a temporary debug signer; the newly generated private signing key has not been provisioned into Actions and was not used for this APK. Exact build commit:ce755aa2b96acdbeadf7ef038e700f79e1a2772d. The Linux binaries and source ZIP match local builds byte for byte. GitHub sign-in is required for Actions downloads; the artifacts expire19November2026 Moscow. Source, evidence and manifests in this branch remain available.

Exact-state tests restored20/20 lanes while retaining all old debt. The extended paired workload measured76.735/35.464Mbps versus RC12's66.246/30.425Mbps, with short-request mean/max249/1125 versus289/1769ms. These are one-pair shared-host lab measurements. CI separately passed at93.339/37.623Mbps and190/311ms; it is not a paired gain or real-provider qualification. RTT spikes, real Speedtest reliability, sustained100Mbps and sub200ms RTT remain unqualified. No qualification threshold has changed.

See [source handoff](../source/HANDOFF.md), [full comparisons including RC2 and rejected bulk experiment](../audit/TUNING_COMPARISON.md), [state/signing guide](../source/docs/STATE_AND_SIGNING.md) and [private key provisioning status/helper](../audit/SIGNING_STATUS.md). New key material remains private and is not provisioned to Actions by this session. An APK's actual signer must be checked independently before attempting an in-place update.

The APK SHA256 and Android identity/native-library report are in [SHA256SUMS](SHA256SUMS), [ANDROID_BUILD_REPORT.json](ANDROID_BUILD_REPORT.json) and [BUILD_VALIDATION.json](BUILD_VALIDATION.json). APK verification is from CI; local large-artifact transfers previously failed, so a second local APK inspection is not claimed. For permanent future signing, use the private provisioning helper described in [SIGNING_STATUS.md](../audit/SIGNING_STATUS.md); no private material is in this repository or the package.
