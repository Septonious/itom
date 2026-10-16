# ITOM Rev7 project handoff

## Release status

ITOM 0.9.2 Rev7 (`transport-rework3-rev7`) is packaged as a **targeted-validation prerelease**. The user explicitly requested stopping broad/repeated tests to conserve Codex usage and finishing the APK, server binaries and project handoff. The 90 Mbps floor is waived. This is not represented as a complete full-suite pass on the final runtime.

Repository: https://github.com/Septonious/itom
Branch: `itom-0.9.2-rev7`
Release: https://github.com/Septonious/itom/releases/tag/v0.9.2-Rev7
Editable project: `archives/rev7/source/` in Git or `itom/` inside the sources ZIP.
Final source SHA-256: `7fd83e66a363bcccec4fd92b8a1f43396a736cbec5e41da0c9304457032c7cac`
Final first-party core runtime SHA-256: `02273833405133fd165e3d1bb183b69148e70a3ca9b658bd3fcb489e57950dde`

The release includes the signed APK, Linux AMD64 and ARM64 binaries, executable source archive, this handoff, paired-capture analysis, validation evidence and checksums. The full ZIP contains all components. `TEST_EVIDENCE.zip` separates baseline full-suite logs from final targeted logs. `STATUS.json` records the qualified source commit, exact APK/native identities and signer. GitHub Actions assembles the tested assets from hashed parts, verifies the source archive and every uploaded asset's size/digest, then publishes the prerelease. Actions assembles these exact tested bits rather than replacing them with an untested rebuild.

## What was verified

The earlier complete core run passed **932 test events**, zero failures/skips, including all 48 field-derived scenarios and mandatory provider-outage cases. IMAP-fork tests and vet passed. That run preceded the final minimum-DATA repair-priority fix. A supplementary repetition exposed a real application recovery timeout; it was not counted as a pass. The final full-suite rerun and expanded repetitions were stopped at the user's request.

Final corrected-source checks:

- The failing correlated-socket-recovery scenario passed three targeted normal repetitions.
- Targeted race checks passed for simultaneous control/receive failure with slow optional LOGINs, correlated control EOF between transfers, and LOGIN cancellation without capacity leaks. No selected test failed or skipped.
- Final Android build passed 81 domain tests and 7 app tests; lint had zero errors/warnings. ARM64 and ARM native libraries were built and verified in the signed APK.
- Targeted `go vet` passed; Linux AMD64 and ARM64 were built from final sources.
- One final 60-second retained-state MULTI load check passed using fifteen accounts, 24 streams in each direction, ten-second warmup, sparse UID history and 6 MiB/130 old message slots retained per account. Every measured second carried useful verified bytes in both directions. Every loaded RTT stayed below 1,100 ms, all thirty probes overlapped duplex traffic, and none missed/timed out. Original TCP identity, session and healthy carrier epochs were preserved.
- After that load, six original-socket exchanges across idle gaps, a fresh verified 256 KiB round trip and another original-socket exchange all passed without restarting either endpoint.

Exact final load metrics:

```text
supervised_load_test.go:281: supervised_result round=1 measured_ms=60000 upload_mbps=116.540 download_mbps=90.160 rtt_samples=30 p50_ms=316 p95_ms=423 max_ms=470 timeouts=0 missed=0 overlap=30 failure=<nil>
supervised_load_test.go:323: supervised_tail round=1 duration_ms=30000 upload_mbps=116.934 download_mbps=91.960
supervised_load_test.go:333: supervised_tail10 round=1 duration_ms=10000 upload_mbps=117.414 download_mbps=91.593
supervised_load_test.go:386: supervised_acceptance round=1 tier=transport-rework3-rev7-stability-throughput-floor-waived minimum_verified_mbps=90.160060 max_rtt_ns=470429865 accepted=true field_qualified=false
```

These are laboratory results. Final full-suite completion, live Rambler and a physical Android device are not claimed. The earlier whole-app mobile crash has no verified fatal stack/root cause. Original first Speedtest failure and the external trigger for the correlated EOFs lie outside the retained paired trace window. `VALIDATION.json` records these boundaries explicitly; it intentionally sets `all_internal_gates_passed=false` and `final_full_suite_completed=false`, while recording successful required targeted checks.

## Transport changes and source map

One logical TCP engine shares bounded credit/memory across independent account carriers. Physical socket repair must preserve that logical session and original application TCP sockets. No reconnect forgives durable physical quota debt.

- `core/tunnel/control_repair.go` and `control.go`: replace recoverable failed physical control sockets without restarting the cipher/carrier or logical TCP streams. Unknown APPEND outcomes remain charged. Replacements use fresh counters and independent physical charges. Permanent authentication/certificate/accounting failures stay fail-closed.
- `core/tunnel/resources.go` and `login_admission.go`: reserve required control capacity at account and endpoint admission, with bounded critical LOGIN priority and cancellation-safe release.
- `core/tunnel/topology.go` and `io.go`: the final fix prioritizes restoring the sole DATA receiver and the first usable DATA writer. Previously they could wait behind optional LOGINs after simultaneous socket failures; with all writers unavailable the scheduler correctly refused DATA work, leaving an apparently reconnected application stalled. Extra parallel DATA writers remain ordinary and paced. Control, receiver and minimum writer restoration now form the required service; this change does not raise connection or quota caps.
- Handshake logic in `core/tunnel/`: repeat the exact authenticated hello after a missing response and use a bounded short-lived server reply cache. Altered/expired/excess replays cannot reset the session.
- Receive logic: after actual repeated missed IDLE notifications, temporarily shorten the active audit. Quiet-account pacing stays bounded. Health diagnostics populate actual stream counts.
- `core/tunnel/grant_recovery.go`, `recovered_shares.go`: use actual remaining byte/message capacity under retained debt, share remaining ordinals fairly and preserve upload refill headroom. Bulk leases contain whole physical BODY units. Earlier bounded cleanup still requires existing authenticated proof.
- `core/journal/origin_view.go`: reuse only exact unchanged persisted rows inside the current authoritative transaction. No cache grants capacity or refunds debt. A rejected full-ledger probe no longer signals a capacity change to itself.
- `core/datapath/pump.go`, `core/tunnel/lane.go`, `delivery_control.go`: logical flight admission follows collection order before parallel physical work. Ready predecessors precede speculative future offsets; a blocked predecessor holds younger records of its own stream while siblings continue. Definitive pre-publication deferrals return immediately. Retry and ordinary handoffs share the original queue bound. The original eight bulk workers remain; increased speculative concurrency was tried and reverted.
- `core/manager/`, `core/servercfg/`, `core/cmd/itom/`: executable, CLI, accounts, clients, messages and diagnostics.
- `core/adminapi/`, `core/adminserver/`, `native/`: authenticated IMAP administration and independent durable rollback supervision.
- `android/app/`, `android/domain/`: dark Android UI, VPN lifecycle, profiles, messages, diagnostics and guarded admin controls.

Detailed mechanisms, capture timing/counts and rejected interpretations are in `docs/rev7/REV7_ANALYSIS_2026-10-16.md` and `docs/rev7/evidence/FIELD_CORRELATION.json`. Raw user captures and provider credentials are excluded.

## Rules to preserve

Count every physical APPEND attempt, including unknown outcomes, against durable byte/message limits. Do not replay an already sealed MIME body as a new uncharged attempt, reset counters, discard old debt or manufacture deletion proof. Cleanup remains authorized only by the existing receipt/deletion/fence rules. Cache entries and scheduler signals are not proof.

Keep physical socket caps, fast/control reserves, logical flight and shared handoff queues bounded. Slow journal/crypto/network work stays outside the ordered collector. Cancellation releases an unspent reservation while retaining published uncertainty. A carrier replacement must not conceal an unexpected healthy-load outage in a test. Preserve original TCP identities during physical repair.

Admin authorization remains server authoritative: an enabled client named exactly `admin` needs its own key challenge. Every reversible mutation arms an independent durable watchdog first. Sixty seconds without verified two-way recovery rolls back; sixty continuously healthy seconds commits. Permanent purges, uninstall, journal migration and full account reset stay CLI-only.

## Build, test and install

Use Python 3.10+ and pinned Go 1.27.1. The build tooling declares/verifies the JDK 17, Gradle 9.3.1, SDK 37 and NDK 28.2 toolchain. From the extracted editable root:

```bash
python tools/build.py server --cache work/build-cache --expect-revision transport-rework3-rev7
python tools/build.py android --cache work/build-cache --expect-revision transport-rework3-rev7 --accept-android-licenses
```

These normal build commands execute their full standard gates. The source ZIP is executable with Python and checks its manifest before extracting a fresh tree. Private signing material is absent; retain the owner's existing signing setup separately if rebuilding an APK that must update the installed app.

The final targeted commands and packaging driver are preserved at `archives/rev7/release-drivers/finish_targeted.py` and `package_targeted_release.py`. They contain this environment's workspace/cache paths; adapt them before use elsewhere. The final load command is:

```bash
python tools/transport_qualification.py --go /path/to/go --accounts 15 --streams multi --seconds 60 --repeats 1 --retained-state --stability-only --results work/final-load
```

Do not run builds, compression or heavy trace analysis alongside timed measurements. The qualification host had a two-core CPU quota. `--stability-only` waives speed alone; integrity, complete byte-clock progress, probes, carrier identity and post-load checks still apply. Omitting it runs the separate historical 90 Mbps policy. For later exhaustive work, `tools/field_qualification.py` retains the full 48-case selection and optional added commit-delay model, but those redundant repetitions are not claimed for this final release.

Update **both app and server** and preserve configuration, journals, wallets and profiles. Use AMD64 on x86-64 Linux or ARM64 on ARM Linux, keep the current state directory, restore executable permission and check `version` reports `transport-rework3-rev7`. The APK keeps published Rev6 application ID `org.itom.client.dev.rev7`, version code 9207 and signing certificate SHA-256 `39667cd3f91521b09af012b2ca08f8f0ba0d7e07f14b32a9a1e794cb3f4bf999`. Install it as an update. `SIGNER.txt` and `SHA256SUMS` verify identity and downloads.

An ordinary upgrade needs no account reset. To intentionally start over, stop the service, use server manager **3. Mail accounts → 6. Reset accounts database**, and type `RESET ACCOUNTS`. Active logical credentials and retired definitions are removed and a fresh ID namespace is assigned. Client/server identities, provider mail and physical accounting remain. Re-add credentials and export replacement profiles to every client before restarting. The reset stays CLI-only; it is not a mailbox purge or disk erase.

Global and individual formatted messages remain in manager menu 17 and the `message` CLI. Individual messages override global; saving/clearing global clears individual entries. Android supports Minecraft-style colors/styles/reset and escaped `&&`. Existing dark UI, removed settings introduction banner and guarded admin controls remain. See `docs/rev5/ADMIN.md` for the control mapping.

## Next owner actions

Prioritize a real-device/provider pass with matching Rev7 endpoints: cold startup, repeated SINGLE/MULTI tests, an existing chat/Telegram connection and a fresh connection after load, extended idle and a Wi-Fi/mobile transition. Capture both endpoints promptly with overlapping clocks and a minute after the failure. Keep captures/profiles private; do not reset accounting or purge mail as a shortcut.

A full final-runtime suite remains optional follow-up under the user's reduced validation scope, not a claimed completed result. Throughput tuning is deferred. Start from the bounded eight-worker state and recorded retained/fresh experiments; inspect useful BODY occupancy, per-account service time and prefix deferrals before increasing concurrency. Preserve all accounting and connection-recovery invariants.

No raw user captures, provider passwords, private profile keys, private signing key or build caches are included in the release.
