# ITOM FIELD61 — restricted spare-capacity bulk scheduling

Release: `FIELD61`. Go revision: `transport-s1-perf7-field61`. Android: `0.9.0-dev2-field61-debug` (versionCode 9061). Base: exact FIELD60 sources, not FIELD58/59 grant-selection experiments.

## Evidence and reason for change

User's real FIELD60 Speedtests: download **2.19 / 5.46 Mbps**, upload **37.5 / 28.5 Mbps**; idle 461/372 ms, download loaded RTT 537/498 ms. Latest client and server traces match FIELD60. Of recorded DATA batches, server s2c: 2081 short/fast batches (about 703 kB physical batch payload), 124 bulk batches (about 13.24 MB physical batch payload). Client c2s: 1155 short/fast, 1136 bulk (about 151 MB). This is a genuine asymmetry in production DATA issuance, not proof of exactly which constraint caused it.

The preexisting scheduler prevented ALL new bulk physical slot reservations whenever `g.fastWaiters > 0`, even when a separate authenticated physical DATA socket was idle. Fast writers occur frequently during HTTPS/multi-TCP loads and this global veto can amplify starvation. The old per-stream logical delivery window and durable grants are intentionally unchanged.

## Source changes

- `core/tunnel/stripe.go`: bulk DATA may reserve an otherwise idle physical socket even while fast DATA is pending, but ONLY if (1) the selected carrier has at least two free physical slots before admission, preserving the local fast reserve; (2) the *live* authenticated session-wide spare capacity exceeds the number of queued fast writers; (3) the carrier is still ready and DataAvailable. All checks occur under stripeGroup.mu, before the physical reservation. A single-socket line, a shrinking live capacity, or a saturated set of lines retains FIELD60's strict fast-first behavior. Existing protected reader carriers remain excluded from new bulk selections.
- `core/tunnel/stripe_spare_capacity_test.go`: regression tests for spare-slot borrowing, multiple fast waiters, and live parallelism shrink.
- Version metadata synchronized for FIELD61, including build, client, server, archive, APK.

NO changes to `delivery_control.go`, `fast_ramp.go`, `lane.go`, IMAP wire format, grant accounting, append debt, gc, physical limits, or durable journals. This is NOT the FIELD58 optimization (which filtered candidate grants by socket availability before grant selection).

## Qualification

PASS: 80 Python source/build tool tests; offline structure and version crosschecks; standalone exact extracted spare-capacity helper with Go1.23 `-race`, 20 runs (including concurrent read-only policy checks); ZIP source manifest SHA256 and launcher `check` PASS (909 source entries; 80 Python tool tests). New in-package Go concurrency tests are included for execution during official build.

NOT RUN: full Go core/race tests and Android Gradle/build (requires Go 1.27.1, Android SDK/Gradle environment); no actual field-speed qualification. Model predictions cannot substitute for user's IMAP provider and Speedtest. This is a *field-test candidate*, not a proven 40/40 Mbps release.

## Build and deploy

Windows PowerShell:

```
python .\ITOM_TRANSPORT_FIELD61_SOURCE.zip all --expect-revision transport-s1-perf7-field61 --accept-android-licenses
```

Expect exported `ITOM_FIELD61_debug.apk`, `ITOM_FIELD61_linux_amd64` and legacy server binary. Deploy Android and VPS from the SAME source revision after all build gates pass, with the durable journals/configuration preserved. The debug Android package is `org.itom.client.dev` and can coexist with release `org.itom.client`. Check `adb shell dumpsys package org.itom.client.dev` for `0.9.0-dev2-field61-debug` and server `itom-linux-amd64 version` for `transport-s1-perf7-field61`.

## First real test / abort criteria

1. Confirm both revisions and 20/20 lines; no account login storms. Make three controlled Speedtests with the same access, destination and similar background load, capture CLIENT AND SERVER diagnostics while traffic is active.
2. Compare download/upload and duration in both directions, idle/loaded RTT, jitter, and the ratio of s2c bulk to fast `data_batch_completion` events, as well as `delivery_admission_wait` and IMAP APPEND failures.
3. Abort and restore a known working paired build if Speedtest drops/stalls, ping or jitter deteriorates materially, grant/journal errors appear, or several postal accounts disconnect. Preserve durable state on rollback.

Never remove on-disk journal/state to force a speed recovery; doing so can break quota/unknown-APPEND reconciliation.
