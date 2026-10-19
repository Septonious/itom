# RC13 server update — existing RC12 Android supported

**Keep the installed RC12 app and existing profile. Update the server first.** Reinstallation/data clearing deletes client wallet history and leaves old grants charged. The RC13 transport correction is server-side and uses the existing RC12 reduced-grant codec.

- [Linux amd64/arm64 binaries](https://github.com/Septonious/itom/actions/runs/39335976534/artifacts/12226424241)
- [Complete buildable source](https://github.com/Septonious/itom/actions/runs/39335976534/artifacts/12227011017)
- [Build run](https://github.com/Septonious/itom/actions/runs/39335976534)

Android is building; this checkpoint records the already verified server/source outputs. Exact build commit:ce755aa2b96acdbeadf7ef038e700f79e1a2772d. The Linux binaries and source ZIP match local builds byte for byte. GitHub sign-in is required for Actions downloads; the artifacts expire19November2026 Moscow. Source, evidence and manifests in this branch remain available.

Exact-state tests restored20/20 lanes while retaining all old debt. The extended paired workload measured76.735/35.464Mbps versus RC12's66.246/30.425Mbps, with short-request mean/max249/1125 versus289/1769ms. These are one-pair shared-host lab measurements. CI separately passed at93.339/37.623Mbps and190/311ms; it is not a paired gain or real-provider qualification. RTT spikes, real Speedtest reliability, sustained100Mbps and sub200ms RTT remain unqualified. No qualification threshold has changed.

See [source handoff](../source/HANDOFF.md), [full comparisons including RC2 and rejected bulk experiment](../audit/TUNING_COMPARISON.md), [state/signing guide](../source/docs/STATE_AND_SIGNING.md) and [private key provisioning status/helper](../audit/SIGNING_STATUS.md). New key material remains private and is not provisioned to Actions by this session. An APK's actual signer must be checked independently before attempting an in-place update.
