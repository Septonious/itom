# ITOM 1.0.0 RC5 — stability-focused testing prerelease

**Release policy updated October 17, 2026:** the owner withdrew the mandatory sustained 100/100 Mbps speed floor. This does **not** waive byte integrity, authenticated mailbox deletion, durable debt, account/session correctness, recovery and post-load TCP survival, or the strict loaded RTT ceiling of **750 ms** in the simulated 10-account qualification.

## Choice based on ALL observed A/B tests

**Selected deployable configuration: RC2-based RC5 transport with the original 12 MiB DATA journal, eight bulk workers, ten provider-wide IMAP sessions per account (already split between endpoints), two protected fast carriers, and bounded authenticated cold-start batching.** No change to physical quota, signing wire format, protocol, journal migration, unknown-APPEND debt or DATA cleanup authority.

The 16 MiB **server-only** / 12 MiB client laboratory setup gave a stronger one-off throughput/RTT balance (**79.399 Mbps download, 141.754 Mbps upload, max 395.830 ms, 30/30 probes**), but it remains an **experimental fresh-state profile** that has not been qualified for existing journals, retained histories or phone/provider deployment. It is excluded from the automatic release defaults.

Representative 10-account 60-second **simulated-provider** reference tests (not repeatability certification):
- Original RC5/12 MiB: download **74.012 Mbps**, upload **152.878 Mbps**, max RTT **498 ms**, carrier/socket survival; other baseline run about 71.5/148.3 Mbps, max 436 ms.
- 16 MiB on both endpoints: 80.845/112.337 Mbps, RTT 547 ms; lower upstream headroom; not selected.
- 16 MiB server / 12 MiB client: 79.399/141.754 Mbps, RTT 395.830 ms; experimental, excluded pending safe upgrade/retained qualification.
- 20/24/32 MiB journal experiments: down 95–113 Mbps but upload frequently 7–46 Mbps late in the test, and one 32 MiB variant exceeded 750 ms. Rejected.
- Global 20 ms RC3 hot polling and RC4 variants have unresolved field throughput/socket regressions; not incorporated.
- Eight bulk workers remain. Higher worker count and extra DATA shards were not beneficial in controlled local runs.

## Mandatory evidence before publishing RC5

- Final immutable source revision, Go1.27.1 native amd64/arm64 and APK identities verified; Android domain/app unit tests and lint actually PASS (not simply parsed).
- Complete unfiltered Go core/server test suite, bundled IMAP fork, vet, native package checks and focused race-mode survival tests PASS.
- Independent fresh and retained-state runs: SINGLE and MULTI, three repeats each, 10 accounts, 60-second overlapping duplex load, modeled IMAP 80ms cost, 10-second warmup, 30/30 RTT probes. Every case passes its built-in **stability-only** mode: positive duplex useful bytes every second, no probe loss, all original and new application TCP connections alive after load. Aggregator checks maximum loaded RTT **strictly less than 750 ms**. 100 Mbps performance floor deliberately not enforced or claimed.
- Publish only the exact binaries/APK compiled and hashed by GitHub Actions against the same source commit. All gate evidence attached and clear LABORATORY_ONLY classification.
- **Real Android/provider and signer compatibility are NOT proven by laboratory CI.** Even after all CI gates pass, publish as **GitHub prerelease/test build**, never stable 1.0.0.
- The GitHub Actions environment may create a different Android debug signing certificate. Compare SIGNER.txt to the installed APK **before installing**. Android rejects a different signing certificate as an in-place update; uninstalling can permanently delete encrypted profiles and secrets. Never uninstall or reset your installed RC2 application as a workaround without separately assured recovery of required secrets.

## Safe update

Preserve user mail accounts, server/client identities, physical mailbox contents, durable journals, wallet debits, and configuration. Never clear or migrate physical debt as part of upgrade. Both endpoints must run the same transport revision `itom-1-0-0-rc5`.

This file is a release **selection policy**, not evidence that the final build, 12-case qualification or Android signer match has already passed. Final runtime gate reports are authoritative.
