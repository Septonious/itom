# ITOM 0.9.2-Rev3-testing source

This publication contains source and investigation notes only, as requested. No APK, server binary, build bundle or artifact publishing workflow is included in this commit.

`ITOM-0.9.2-Rev3-testing-sources.zip` contains the complete executable source archive. `source/` contains all modified/new files relative to Rev2, and `REV2_TO_REV3.patch` is the reviewable patch against that baseline. The complete archive remains byte-identical to the source validated during this investigation. Its internal source revision is `transport-rework3-rev3`; the testing label identifies this GitHub publication.

Read `source/docs/rev3/REV3_TRANSPORT_REWORK.md`, `REV3_FIELD_FINDINGS_AND_RESULTS.md`, and `REV3_VERIFICATION.md` for the multiplex scheduling, repair, feedback and load-control changes and retained results. Correctness checks passed. All six sustained laboratory runs completed without stalls and with application RTT below 511ms, but the complete 100/100 Mbps throughput qualification failed. Real Android/provider field qualification was not run. These results do not establish real-world Speedtest performance.

Raw diagnostics, account mappings and signing keys are excluded. See `manifest.json` for source archive and file checksums.
