# ITOM 0.9.2 Rev8 — source-only development candidate

Based on the exact published Rev7 source; Rev7 release files are unchanged.

## Scope

This is a deliberately conservative first Rev8 change, not yet the speed-target release. Analysis of the October 16 paired Android/Linux capture showed high useful throughput (81.2–90.7 Mbps download and 40.5–50.6 Mbps upload), but incomplete failed-Speedtest history due to rotating trace files. A burst of 142 `WALLET_EXHAUSTED` physical spends was observed in the client during upload. The source proves that this outcome is the intentionally rollback-safe grant-rollover path: a cumulative lease cannot fit the next complete batch, so the wallet rolls back, the grant fences, and the original unsent logical batch retries using another grant. Do not interpret every exhaustion as a lost payload, reset quota, or weaken durable proof.

Changes:
- Runtime revision `transport-rework3-rev8`, Android `0.9.2-Rev8` / versionCode `9208`, same debug application ID `org.itom.client.dev.rev7` (signing key is not published).
- Bounded cumulative per-lane `grant_rollover` trace events: a maximum of roughly four events/second for repeated wallet exhaustion per lane. Each contains total events, events since last sample, records, class and encrypted batch size. No physical identifiers, plaintext, destination or credentials are logged.
- Preserve full `data_batch_completion` and all non-exhaustion `data_batch_spend` events. Only redundant `WALLET_EXHAUSTED` spend-detail events are sampled, avoiding diagnostic ring churn under heavy MULTI.
- Sampling regression test; no changes to packet encryption, journal or wallet spend/rollback semantics, quotas, retry scheduling, transport wire or protocol.
- Build and identity validation updated for Rev8.

## Validation state

No local Go 1.27.1 / Android SDK37/NDK toolchain or private signing key was available inside the authoring environment. Thus no final test, APK or Linux binaries have been built from these changes. It must be treated as an unqualified development candidate, not an internally or field qualified release. Do not publish a production release or claim improved throughput until the paired actual-device tests pass.

## Build (Linux or macOS shell, with git, Python 3.10+, JDK 17, internet and platform SDK dependencies)

```sh
git clone --filter=blob:none --sparse --branch itom-0.9.2-rev8 https://github.com/Septonious/itom.git itom-rev8
cd itom-rev8
git sparse-checkout set archives/rev8/source
cd archives/rev8/source
python3 tools/build.py all --expect-revision transport-rework3-rev8 --accept-android-licenses
```

Install matching Rev8 client and server using existing safe upgrade procedures; preserve server profiles, physical provider mail, wallet/journal debts and private keys. The locally built APK might use a different debug signer. Check before updating the installed app, and export profiles before any unavoidable reinstall.

For testing: record actual Speedtest error, matching client/server diagnostics, exact test time, and ensure original and fresh application sockets still work after aborted MULTI tests. Compare with matching Rev7 field results under the same provider and target.
