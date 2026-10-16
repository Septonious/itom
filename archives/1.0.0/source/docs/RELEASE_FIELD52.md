# ITOM FIELD52 — экспериментальный guarded capacity probe

Date: 2026-10-09. Revision: `transport-s1-perf7-field31`, Android versionCode 9011.
Source archive: `ITOM_TRANSPORT_FIELD52_SOURCE.zip`.

## Why

Actual Android Speedtest on a stable raw 130Mbps connection did not reach 20Mbps in FIELD51, while RTT was close to 500ms under load AND idle. The historical FIELD51 synthetic 130Mbps measurements produced only ~9.1Mbps down and ~8.9Mbps up in separate phases; these are not field measurements. Wider fixed windows in development experiments improved throughput but hurt tail latency and violated the 20Mbps / 1500ms acceptance gate. Preserving the stable carrier and RTT takes priority.

## Change, with limits

- FIELD51 delivery byte accounting, independent 128KiB interactive reserve, original ACK receipt rules, physical IMAP permits, journal debt, 'unknown APPEND' semantics, physical workers and mail reader logic are preserved.
- New `capacityProbe` implements limited 128KiB bulk flight trials only at a throughput plateau after **3 decision epochs** with useful ACK rate >=8.75Mbps, at least four fresh residence samples, sustained demand, and DATA residence within the learned target's 25-100ms headroom band. No automatic increase on 7–8Mbps slow links. No blanket 2/3MiB startup window change.
- A trial compares useful ACK throughput over 4s with the rate sampled at entry and keeps the step only for >=8% useful improvement. Otherwise it rolls back to the *prior logical* budget (not physical debt) and enters a cooldown. Accepted probes have a 12s cooldown, rejected 16s.
- Existing FIELD51 age pressure still shrinks 1/8 and cancels trials; the pilot also cancels on pending fast ACK >750ms, recent completed fast ACK >750ms, or fresh fast ACK regressing by >100ms relative to its preprobe sample. Active probe requires ongoing useful ACK progress. No trial is performed without measured demand; idle budget does not inflate.
- Diagnostic DTO adds `ProbeAttempts`, `ProbeAccepted`, `ProbeRejected`, `ProbeActive` so live traces show if the candidate actually activates. If these remain zero, this specific change cannot be credited with increased throughput.
- Initial bulk window 1MiB; minimum 768KiB; maximum 3MiB; short reserve 128KiB. No wire change. Delivery credit only returned on authenticated progress/terminal contract.

## Verification performed

- `GO111MODULE=off GOTOOLCHAIN=local go test capacity_probe.go capacity_probe_test.go -count=1` — PASS (4 tests, Go 1.23.2).
- Same tests under `-race` — PASS.
- `python tools/check_sources.py` — PASS (34 launcher, 41 core-tools, fork pins/metadata/XML validation). 60 Kotlin tests *identified*, not executed.
- Offline integration compilation against minimal local interface stubs: 6 tests (including delivery-controller virtual-clock integration), both normal and `-race` PASS on Go 1.23.2. These stubs do NOT substitute for the full production Go module.
- `gofmt` and Python source validation: completed.

NOT RUN: full Go 1.27.1 core/tunnel suite, integration tests in `delivery_capacity_probe_test.go`, Go vet, model20/model130 transport, native compilation, Kotlin/Gradle, Android APK, device/VPS, real provider & ordinary Speedtest. The current environment lacks the pinned Go 1.27.1 toolchain and cannot reach the network to install it. No claim of achieved speed or RTT regression-free acceptance is made. Original FIELD51 remains stable rollback.

## Strict unchanged acceptance

- On the 20Mbps full-duplex model, each active useful direction and its final 20s tail >=5Mbps, max HTTPS application RTT <=1500ms, errors zero; no tolerance.
- On the real 130Mbps physical connection, measure ordinary Speedtest down/up concurrently, idle RTT, loaded RTT, number of TCP failures, mail line exits and 10 account states; compare on same connection FIELD51 vs FIELD52.
- TARGET: at least 25/25Mbps useful *real* Speedtest without increasing the ~500ms load RTT. The current experimental pilot does not guarantee this and has not been benchmarked on target hardware.

## Windows build

```powershell
python .\ITOM_TRANSPORT_FIELD52_SOURCE.zip all --expect-revision transport-s1-perf7-field31 --accept-android-licenses
```

Linux VPS after successful build (do not erase data/journals):

```sh
chmod 755 /tmp/itom-linux-amd64 && sudo /tmp/itom-linux-amd64 install
```

Run selected Go models if needed with `model20` and `model130` before device rollout. Both ends MUST run `field31`; preserve user profile, journal and mail folders; keep FIELD51 for rollback. Keep original client/server logs before ring-buffer wrap. Do not redeploy if `all` or 20Mbps gate fails.
