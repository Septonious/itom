# ITOM FIELD72 — build/output fix (test candidate, 2026-10-10)

## Scope / baseline

- Restored FIELD68 production transport, including the FIELD68 test-gate correction.
- Carry forward the 30/30 Mbit/s test gate, 24+24 independent TCP streams,
  after a 10-second warmup and 30 seconds of useful-data measurement.
- Every RTT probe must be strictly below 800 ms; no probe timeout, no byte-integrity
  failures, no 5-second progress stalls; final consecutive 10 seconds must also pass.
- Prior three comparable synthetic runs (before this packaging repair) were:
  30.12/26.83 (RTT 533 ms), 26.35/27.15 (584 ms), and partial reserve experiment
  26.45/27.81 (586 ms). All fail 30/30. The reserve experiment is excluded.
- This candidate does NOT incorporate the unqualified FIELD70/71 multi-owner changes.

## Changes

1. Consolidated release numbers (`FIELD72`, server `transport-s1-perf7-field72`,
   Android versionCode 9072, debug versionName `0.9.0-dev2-field72-debug`).
2. Fixed executable source ZIP artifact discovery: successful `all` now exports a
   checked APK, APK checksum, compiled server binaries for amd64/arm64, and build
   reports into `dist/` next to the source ZIP. Previously these were only in
   an unpredictable `ITOM_<revision>_*/itom/dist/` extraction directory.
3. Only successful, matching revision artifacts with matching SHA-256 sums are
   published. Partial or failed Android builds do not create a public APK.
4. Added export success/invalid-checksum/incomplete-binary regression tests.
5. Kept the 30 Mbps / <800 ms speed acceptance untouched; did NOT weaken tests.

## Build/update commands

Build on Windows PowerShell in the source ZIP directory:

```powershell
python .\ITOM_TRANSPORT_FIELD72_SOURCE.zip all --expect-revision transport-s1-perf7-field72 --accept-android-licenses
```

On success, find APK at `dist/ITOM_FIELD72_debug.apk` and server binary at
`dist/ITOM_FIELD72_linux_amd64`. A successful `server` command does not build
an APK. When running the source ZIP, `dist` is next to the ZIP (not the random
extraction directory). Logs remain in the extracted build tree.

Server update (one PowerShell command; replace `YOUR_VPS`):

```powershell
scp .\dist\ITOM_FIELD72_linux_amd64 root@YOUR_VPS:/tmp/ITOM_FIELD72_linux_amd64; if ($LASTEXITCODE -eq 0) { ssh root@YOUR_VPS "chmod 755 /tmp/ITOM_FIELD72_linux_amd64 && /tmp/ITOM_FIELD72_linux_amd64 version && /tmp/ITOM_FIELD72_linux_amd64 install" }
```

Do not install without testing the client and server with matching revisions,
backing up durable state, and proving the current state is format-compatible.
Especially do not clear or silently migrate old FIELD67 BINARY journal debt.

## Qualification

Go, Python and Linux tests run on local offline Go 1.27.1; see accompanying
validation log for exact pass/fail status. Android SDK/Gradle is NOT available
in this environment, so no APK has been compiled or fabricated. The launcher
now exports it if and only if the real Android gates succeed on Windows.

**NOT field-qualified / NOT a production performance release:** the 30/30
Mbit/s acceptance condition is not yet proven for this candidate. Real provider
and Android on-device acceptance remain outstanding.

## Current-turn validation results (2026-10-10)

- `python tools/check_sources.py`: PASS.
- `python -m unittest test_launcher test_core_tools`: **83 PASS**, including
  new artifact publication tests and rejection of mismatching checksums.
- Exact executable source ZIP `check`: PASS (83 Python cases).
- Go 1.27.1 on linux/amd64: full `go test -mod=readonly -count=1 -timeout=220s ./...`
  **PASS, 19 packages**, tunnel 163.386 seconds.
- `go vet -mod=readonly ./...`: PASS.
- Go race tests `./datapath ./journal ./mailbox`: PASS, 2 iterations.
- Linux amd64 and arm64 statically linked server build: PASS.
- **Strict 24+24 TCP/IMAP/TLS speed gate: FAIL**, full 30-second measurement:
  upload **26.153 Mbps**, download **28.250 Mbps**, final 10 seconds
  upload **25.325 Mbps**, download **27.737 Mbps**; RTT max 522 ms,
  30/30 probes, zero probe timeout/miss, 10 accounts, modeled 130 Mbps,
  65ms command delay. The 30/30 speed threshold was not attained.
- Android APK build and on-device verification: NOT RUN (SDK, Gradle and
  Android toolchain dependencies not available in this offline runtime).
- No live Rambler/IMAP field qualification. **Do not deploy as a production
  speed-qualified release.**
