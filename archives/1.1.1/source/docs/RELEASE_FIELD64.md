# ITOM FIELD64 — standard Go-gate classification fix

FIELD64 fixes the Windows `all` failure introduced in FIELD62/FIELD63:
`TestSpeedtestMultiflowAcceptance` is explicitly opt-in (`ITOM_SPEEDTEST_GATE=1`),
so the standard Go test reports SKIP when that environment variable is absent.
The strict `tools/build_core.py` classifier did not whitelist this named optional
integration test and incorrectly rejected an otherwise completed `core` run.

The classifier now permits SKIP **for that exact test name only**. It still
rejects a failure for that test and every unexpected skip/failure, and reports
the named optional SKIP transparently. New Python regression tests cover
PASS, SKIP and FAIL classifications.

The first full Linux Go replay exposed a second, independent fixture race in
`provider_cap_recovers`: background IMAP workers could fill the synthetic
provider's nine-session limit between the setup's check and its next login.
The fixture now retries only a *confirmed* transient provider-cap refusal,
with a finite two-second deadline; other authentication/connection errors
continue to fail. It does not modify production IMAP recovery logic or
weaken the provider-cap recovery checks.

The complete production Go transport and the Speedtest 24+24 TCP workload are
unchanged from FIELD63. This patch does not claim field 40/40 Mbps acceptance.

Build on Windows:

```powershell
python .\ITOM_TRANSPORT_FIELD64_SOURCE.zip all --expect-revision transport-s1-perf7-field64 --accept-android-licenses
```

The `all` target performs the regular mandatory Go/Kotlin/Android gates. It
**does not run the opt-in 24+24 profile**. For separate qualification, with
Go 1.27.1 available, run inside the extracted `itom` directory:

```powershell
python tools\run_speedtest_gate.py --profile normal --streams 24 --seconds 30 --log-dir reports\speedtest-field64
```

Do not deploy an APK if `all` fails. Android/VPS must use the same revision.
