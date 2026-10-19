# ITOM 1.0.0 RC12 — field-testing candidate

Baseline: packaged RC11 source 4de97a77ed5d0c2ff89e25212e78f5b840cfcade. Android identity remains org.itom.client; version code 10012, name 1.0.0-rc12-debug. Install matched client/server builds and preserve their existing state.

## Correction

A durable usage report that spends a fast grant's final message permission now marks that grant unusable immediately. Its replacement can be issued before the separate fence arrives, only if the existing journal allocator has genuine capacity. The original grant stays open and fully charged until its fence and exact physical deletion proofs arrive. Immutable issued limits are cached to avoid adding a journal read to each fast report, and removed with existing grant retirement/cleanup.

Under retained historical debt, fast recovery leases use 256 KiB rather than a maximum 512 KiB bulk BODY. A stable directional message target prevents repeated refills shrinking solely because bulk reservations already exist. Recovery bulk allocation preserves two refill ordinals and bounded byte headroom. Existing directional shares, normal grant sizes, physical profiles, wire format, durable proof authority and qualification thresholds are unchanged.

## Targeted evidence

A regression fails on RC11 and the earlier refill-only candidate: a fully message-spent fast grant still counts as usable while its fence is in flight. The final candidate passes with a replacement available while the original grant remains open and unrefunded. Valid partial physical deletion proofs restore a multi-message grant; historical unknown debt stays charged. Actual sealed maximum fast records and envelope/MIME sizing are exercised.

The phased reference uses ten accounts, a 10 ms/500 Mbps client route and 20 ms/1 Gbps server route, 7.5 MiB/150 retained remote ordinals per account, real TLS/IMAP and fsync, plus the observed 20 ms server commit service cost. Eight independent streams transfer 4 MiB each in download then upload, alongside persistent short requests. Final RC12: 53.427/38.115 Mbps, short-request mean/max 244/557 ms. RC11: 46.440/28.157 Mbps and 248/844 ms. Both passed. Three earlier tuning variants were rejected for download or latency regressions.

These are limited lab measurements on a shared two-CPU test process, not a statistical performance guarantee or an eight-core CPU emulation. Real Speedtest failure resolution, sustained 100 Mbps and sub-200 ms RTT remain unqualified. Full qualification tools and thresholds are preserved.

See ../audit/FOLLOWUP_FIELD_FINDINGS.md and TUNING_COMPARISON.md for the field evidence and discarded candidates. The repository workflow .github/workflows/itom-rc12-field-test-build.yml gates matched APK/Linux/source builds with focused integrity, quota/proof, retained-state, phased transfer and race checks. APK signer, test/lint reports and SHA256SUMS accompany the artifacts.

Do not clear client wallets, app data, server journals or identities to work around an APK signer mismatch. Missing client wallet history leaves old grants charged on the server. A stable private APK signing key is required for reliable in-place updates; compatibility with a previous debug APK is not assumed.
