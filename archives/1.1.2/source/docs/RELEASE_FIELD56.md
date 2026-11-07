# ITOM FIELD56 — staged optional DATA initialization

**Revision:** `transport-s1-perf7-field35`, Android versionCode 9016.  
**Base:** exact FIELD55 `field34` project. **Experimental / not field-qualified.**

## Real user results (FIELD55)

Two successive Speedtests: download **33.0 / 35.4 Mbit/s**, upload **21.7 / 22.2 Mbit/s**. Idle RTT **739 / 417 ms**, loaded download RTT **425 / 389 ms**, loaded upload RTT **339 / 348 ms**, jitter **199 / 104 ms**. Compared with prior FIELD52 22.5/25.9 and 384 ms idle, peak download improved but upload and idle/jitter stability degraded. Goal is 40/40 without damaging RTT. This release does not change delivery-rate policy.

Both logs are FIELD55: Android `0.9.0-dev2-field34-debug`, server `transport-s1-perf7-field34`. The client ring interval 2026-10-08T23:50:51–23:52:47 UTC contains 129,110 events. Server ring 23:51:16–23:53:14 UTC contains 113,044 events. Both are mid-session ring traces and **do not contain first IMAP connection / all-20-ready startup timing**, so the startup optimization's field impact is not established.

Client IMAP APPEND DATA: 2,110 completed OK, median 67.0 ms/p95 138.4 ms. Server: 2,555 OK, median 73.9 ms/p95 135.9 ms. Observed high-frequency deferred DATA admission is ordinary flow control, not standalone carrier failures. Maximum internal ObservedRTTMS 1641 client / 1055 server (not the Speedtest ping); internal windows max 1,327,104 client / 2,519,987 server. The samples cannot isolate a unique upload-rate bottleneck; changing the window/RAMP without a full model could regress again.

## Actual FIELD56 changes

1. The Android client's per-account connection supervisors remain parallel (10 profiles x 2 logical lines = 20). During each lane's initial `Activate`, the base, reader, one DATA writer, and maintenance sockets are still opened and authenticated as before. The **second, optional DATA writer** no longer delays the initial lane publication. A dead placeholder is registered instead.
2. The *existing* bounded/pace-controlled `repairDataWriters` worker opens that optional second socket in the background (starts after approximately 1 second with existing jitter). Until authentication succeeds, `DataParallelism` advertises **one**, not fictitious two; after success the existing wake callback expands physical capacity. If the connection fails, the original capped backoff/fallback applies. This does not add TCP connections or change provider connection limits.
3. Client/server per-line diagnostics now retain `InitialReadyMS`: duration from the most recent supervisor connection attempt start through first application gate READY. This value remains in health snapshots after older startup trace entries rotate out. It resets on a new attempt. It is not an IMAP-only handshake measurement.
4. **Unchanged:** delivery_controller, fast_ramp, stripe scheduler, quotas, on-wire format, journals, encryption, IMAP connection limits, GC and base socket behavior; server shared DATA worker initialization is unchanged.

## Validation status

- Go `gofmt` on changed source, Python launcher **34 tests**, core tools **41 tests**, offline source/config gates PASS.
- New Go regression tests are included for staged writer activation and capacity recovery, close-before-repair, and snapshot startup timing. **Not executed**, because local Go is 1.23.2 and production core mandates Go 1.27.1.
- Go 1.27.1 full core, model20/130, Android APK/NDK/Gradle, actual startup-time and speedtest A/B: **NOT RUN**. No speed, RTT or startup improvement claimed as measured.

**Deployment gate:** do not deploy if the Windows `all` command fails. Use both sides on the same revision. Keep FIELD55 for immediate rollback. Compare at least three cold starts: time to 20/20, `InitialReadyMS` per account, physical IOConnection count 30s later; then download/upload, idle/loading ping and jitter. Restore FIELD55 if completion is slower, upload falls, idle jitter rises, or Speedtest disconnects. In case of failure export both traces before stopping VPN.

Windows PowerShell:

```powershell
python .\ITOM_TRANSPORT_FIELD56_SOURCE.zip all --expect-revision transport-s1-perf7-field35 --accept-android-licenses
```
