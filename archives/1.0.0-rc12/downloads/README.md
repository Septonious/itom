# ITOM 1.0.0 RC12 — matched field-testing build

[Download APK, Linux amd64/arm64 binaries and source together](https://github.com/Septonious/itom/actions/runs/39310282404/artifacts/12216865976).

| Component | GitHub artifact |
| --- | --- |
| Android APK + public signer certificate | [Android](https://github.com/Septonious/itom/actions/runs/39310282404/artifacts/12216741105) |
| Linux amd64 and arm64 server binaries | [Linux](https://github.com/Septonious/itom/actions/runs/39310282404/artifacts/12215268637) |
| Complete buildable source ZIP | [Source](https://github.com/Septonious/itom/actions/runs/39310282404/artifacts/12215711045) |

GitHub sign-in is required for Actions downloads. Build artifacts expire on 18 November 2026; the source, evidence and manifests in this branch remain available. [Successful build run](https://github.com/Septonious/itom/actions/runs/39310282404). Exact build commit: `270b4656168b6298ab9411f2c8a4bfca29f874ee`. Subsequent handoff commits only update audit/evidence/download documents, not executable source.

## Install and preserve state

Update both Android and server to this matching build. Android application ID remains `org.itom.client`; version code 10012, version name `1.0.0-rc12-debug`. The APK is debug signed. Its public certificate is recorded in `SIGNER.txt` inside the Android and combined artifacts; compatibility with an installed APK is unverified. Do not uninstall or clear app/profile data to bypass a certificate mismatch. Preserve client wallets, account IDs and server journals: missing client wallet history leaves old server grants charged. No quota reset or unproved refund is part of RC12.

Verify the four main files with the committed [SHA256SUMS](SHA256SUMS). The combined package also contains its generated `SHA256SUMS` and testing notes.

## Changes and results

RC12 replaces a fully message-spent fast grant as soon as its durable usage report arrives, before its separate fence, using only genuine allocator headroom. The original grant stays open and fully charged. Reduced recovery grants retain bounded fast-refill capacity and avoid shrinking repeatedly to one message. Normal grant shapes, physical limits, proof authority and qualification thresholds are unchanged.

Paired retained-state phased workload: RC11 46.440/28.157 Mbps download/upload and short-request mean/max 248/844 ms; RC12 53.427/38.115 Mbps and 244/557 ms. Three earlier variants were rejected for throughput or latency regressions. These are single-pair measurements in a shared two-CPU test process, not a performance guarantee. The independent CI workload passed at 92.932/40.022 Mbps and 188/253 ms; it is unpaired and cannot establish a comparative gain.

Focused quota/proof, journal, recovery, phased transfer, race and source-integrity checks passed. Android executed 82 domain and 7 app unit tests, with zero failures. Lint has zero errors and one dependency-version advisory. CI verified package identity, both JNI revision markers, APK signature and combined-package hashes. The Linux binaries and source ZIP also match the local outputs byte for byte. Local repeated APK download failed with workspace disconnections, so the second APK inspection relies on CI verification recorded in [ANDROID_BUILD_REPORT.json](ANDROID_BUILD_REPORT.json).

This is a field-testing candidate. Real-provider Speedtest reliability, sustained 100 Mbps and sub-200 ms RTT remain unqualified; no threshold has been waived. See [source handoff](../source/HANDOFF.md), [capture findings](../audit/FOLLOWUP_FIELD_FINDINGS.md), [tuning comparison](../audit/TUNING_COMPARISON.md) and [machine-readable build evidence](BUILD_VALIDATION.json).
