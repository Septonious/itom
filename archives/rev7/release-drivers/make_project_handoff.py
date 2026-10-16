"""Write the public Rev7 project handoff from completed qualification evidence."""
import json
from pathlib import Path

WORK = Path('/workspace/work/rev7')
ROOT = WORK/'source/itom'
q = json.loads((ROOT/'docs/rev7/evidence/rev7_validation.json').read_text())
assert q['all_internal_gates_passed'] and q['internally_stability_qualified']
assert q['throughput_floor_waived_by_user'] and len(q['pipeline_steps']) == 9
rows = []
for state, key in [('Fresh', 'speed_cases'), ('Retained', 'retained_speed_cases')]:
    for case in q[key]:
        result, accepted = case['supervised_result'], case['supervised_acceptance']
        rows.append(f"| {state} {case['name']} | {result['upload_mbps']} | {result['download_mbps']} | {float(accepted['minimum_verified_mbps']):.2f} | {int(accepted['max_rtt_ns'])/1e6:.1f} | 30/30 |")

text = f'''# ITOM Rev7 project handoff

## Release state and scope

ITOM 0.9.2 Rev7 (`transport-rework3-rev7`) is internally qualified for stability and packaged as a field-validation prerelease. **Every required internal gate passed.** The user explicitly waived the 90 Mbit/s floor and prioritized connections remaining usable after load. Rates are measured and reported; no throughput floor or live-provider performance certification is implied.

- Repository: https://github.com/Septonious/itom
- Development and release branch: `itom-0.9.2-rev7`
- Release: https://github.com/Septonious/itom/releases/tag/v0.9.2-Rev7
- Complete editable project: `archives/rev7/source/` in Git, or `itom/` inside the executable sources ZIP.
- Qualification source SHA-256: `{q['qualification_source_sha256']}`
- First-party core runtime SHA-256: `{q['first_party_core_runtime_sha256']}`
- Baseline: published Rev6. Earlier archives remain unchanged.

`STATUS.json` identifies the qualified source commit, signing certificate and exact runtime/APK identity. `VALIDATION.json` records all gate outcomes. `SHA256SUMS` covers the attached components; the full ZIP has its own checksum. `TEST_EVIDENCE.zip` retains the laboratory event logs and per-run reports used for the aggregate evidence. The GitHub Actions run assembles the already-tested bits from hashed parts, checks source and artifact identity, uploads a draft, verifies every remote size/digest, then publishes. It does not substitute an untested rebuild for the qualified APK or binaries.

## What the field captures establish

The paired Rev6 capture shows all fifteen client carriers exiting following physical control APPEND failures. Later read EOF, uncertain APPEND completion and closed-socket errors belong to that cascade. The retained server trace does not cover the initial remote trigger, so an external timeout, provider policy, fixed byte threshold or daemon restart is not established. The first failed Speedtest is also outside the useful paired window. The delayed final account pair includes a missing handshake reply and later retry; this does not prove every earlier 28/30 startup had the same cause. The old health field also counted ready carrier pairs rather than thirty underlying services.

The provider/account startup errors were not all bad passwords. Rev6 introduced bounded optional sockets, keepalive handling and safe LOGIN response categories. Rev7 additionally reserves critical repair capacity and prioritizes required authentication ahead of optional sessions. No provider credentials are changed automatically.

The earlier Wi-Fi download ceiling was corrected by the user as an ISP router issue. It should not be used as evidence of a transport cap. Whole-app mobile crash root cause remains unconfirmed because no corresponding fatal stack was available. See `docs/rev7/REV7_ANALYSIS_2026-10-16.md` and `docs/rev7/evidence/FIELD_CORRELATION.json` for trace counts, timing, limits and the repaired mechanisms.

## Architecture and code map

| Area | Source | Responsibility |
| --- | --- | --- |
| Public client/server supervisors | `core/tunnel/` | One logical TCP engine across independent account carriers; connection startup, stream identity, recovery and mailbox roles |
| Control repair | `core/tunnel/control_repair.go`, `control.go` | Replace failed physical sockets while retaining logical sessions, cipher state and stream ownership |
| Critical resource admission | `core/tunnel/resources.go` | Per-account and endpoint socket reservations, bounded LOGIN admission, required repair priority |
| Multiplexing | `core/datapath/pump.go`, `core/tunnel/lane.go`, `delivery_control.go` | Ordered logical admission followed by parallel physical work; predecessor retry priority; bounded flight and queues |
| Grant recovery | `core/tunnel/grant_recovery.go`, `recovered_shares.go` | Recover actual remaining byte/message capacity without forgiving historical charges |
| Durable accounting | `core/journal/`, `core/wallet/`, `core/mailbox/`, `core/flow/` | Physical spend, uncertainty, authenticated receipt and deletion/fence proof |
| Core executable and CLI | `core/cmd/`, `core/manager/`, `core/servercfg/` | Server lifecycle, configuration, accounts, clients, messages, diagnostics and administration |
| Admin RPC and watchdog | `core/adminapi/`, `core/adminserver/`, `native/` | Authenticated IMAP commands and independent durable rollback supervision |
| Android | `android/app/`, `android/domain/`, `native/` | Dark UI, VPN lifecycle, profiles, diagnostics, server messages and guarded admin controls |
| Build and qualification | `tools/` | Pinned builds, source verification, field scenarios and complete repeatable laboratory load gates |

## Repairs carried by Rev7

1. Physical control EOF/reset/timeout and recoverable IMAP failures repair the control socket rather than destroying every logical stream. Publication and shutdown are synchronized. Original uncertain APPEND debt remains charged; retries use new cipher counters and independent physical charges. Definitive credential/certificate errors and poisoned accounting stay fail-closed.
2. Both account and global admission reserve capacity for required control roles. Control/receive repairs, the sole DATA receiver and the first usable DATA writer can pass queued optional LOGINs, including mixed layouts constrained by provider socket caps. An additional repetition exposed starvation of minimum DATA service; the corrected recovery check passed three focused repetitions before the final complete build. Extra parallel DATA writers remain optional.
3. Missing handshake replies recover through bounded repeats of the exact authenticated hello and a short-lived server reply cache. Altered, expired or excessive replay cannot reset a session.
4. Repeated actual missing IDLE notifications temporarily shorten active audit delay. Quiet accounts retain bounded ordinary pacing. Carrier traces report actual stream count.
5. Retained physical debt is preserved while remaining message ordinals are shared fairly between directions. Recovery leases contain complete physical bulk BODY units, leave upload refill capacity, and schedule earlier bounded cleanup when remaining capacity is small. Real proof remains required to reclaim anything.
6. Journal origin reads cache only exact unchanged persisted rows inside the current authoritative transaction. Cache contents cannot admit a write, refund debt or invent capacity. A failed full-ledger probe no longer wakes itself as though capacity had changed.
7. Bulk logical admission follows FIFO collection order before parallel disk/crypto/network work. Ready unsent predecessors take priority over younger speculative offsets. A waiting predecessor holds younger records of the same stream while siblings continue. Definitive pre-publication deferrals return immediately instead of waiting for an unrelated slow APPEND. Ordinary and retry handoffs share the original queue bound. The original eight bulk workers remain; increasing speculative concurrency was tried and reverted.

## Invariants for future development

- Preserve original application TCP identities and logical session state across physical socket repairs. A quick carrier replacement must not conceal an unexpected outage in a healthy-load test.
- Count every physical APPEND attempt, including unknown outcomes, against the durable byte/message limits. Reconnecting, timing out, replaying or losing a completion never refunds physical debt.
- Never replay an already sealed MIME body as a new uncharged attempt or reset cipher counters. Stored-but-unacknowledged APPENDs require a distinct charged replacement.
- Authorize cleanup only through the existing authenticated BODY receipt, deletion and fence rules. A read cache or scheduler signal is not proof.
- Keep fast/control capacity and physical connection caps bounded. Preserve the existing hard logical flight and shared handoff memory bounds.
- Keep slow work outside the ordered batch collector. Cancellation must release an unspent reservation without removing a published uncertain charge.
- A successful aggregate cannot conceal a failed, skipped or incomplete required run. Source identity must match before and after every run and the packaged runtime must match the tested build.
- Admin authorization is server authoritative. An enabled client named exactly `admin` still needs its own key challenge. Every reversible mutation arms the independent durable watchdog first. Sixty seconds without verified two-way recovery rolls it back; sixty continuously healthy seconds commits it. Permanent purge, uninstall, journal migration and full account reset remain CLI-only.

## Completed validation

All nine final pipeline stages passed on the source fingerprint above:

- {q['core']['passed_test_events']} core test events, zero failures and zero skips; mandatory provider-outage scenarios enabled; bundled IMAP-fork tests and vet passed. Linux AMD64 and ARM64 were built.
- The complete 48-case field-derived suite within the mandatory core run, with real fsync. At the user’s request, separate repeated field suites and the added commit-delay experiment were removed from final qualification. Tests cover original chat sockets during repeated transfers, simultaneous physical control/receive failures, slow optional LOGINs, capped mixed layouts, missing handshake replies, stored APPENDs whose completion is lost, retained debt through 188/192 slots, missed IDLE notifications and 110 seconds of idle before a subsequent exchange.
- Accounting/datapath, recovery and native admin race gates passed; native vet passed. The native administrative restart scenario recovered after {q['admin_features']['server_restart_recovery_ms']} ms, within the independent sixty-second rollback interval.
- {q['android_domain_tests']['tests']} Android domain tests and {q['android_callback_adapter_tests']['tests']} app tests passed; lint {q['android_lint']['errors']} errors and {q['android_lint']['warnings']} warnings; both supported native ABIs built and were verified inside the APK.
- Four independent real-local-TLS/IMAP supervisor load runs: one SINGLE and one MULTI on fresh state, repeated with 6 MiB and 130 old message slots retained per account. Each uses fifteen accounts, 64 actual prior UID allocations per DATA mailbox, a ten-second warmup and sixty-second measurement. MULTI has 24 streams per direction.

Every measured second made useful progress in both directions. Every loaded RTT stayed below 1,100 ms; all thirty scheduled probes overlapped duplex traffic with no miss or timeout. Original TCP identities, logical sessions and healthy carrier epochs survived. After load, every run passed six original-socket exchanges separated by idle gaps, a fresh verified 256 KiB round trip, and another exchange on the original socket, without restarting client or server.

| Scenario | Upload Mbit/s | Download Mbit/s | Lowest full/30s/10s rate | Maximum loaded RTT ms | Duplex probes |
| --- | ---: | ---: | ---: | ---: | ---: |
{chr(10).join(rows)}

These are local laboratory rates and synthetic fault scenarios, not a guarantee of speed on a live IMAP provider. Earlier exploratory failures remain failures and do not count toward the final qualification. The interrupted expanded matrix is not counted as a pass. Its minimum-DATA recovery timeout was repaired and the failing scenario was rerun; only final same-source passing runs are included above.

## Build and reproduce

Use Python 3.10+ and the pinned Go 1.27.1. The project build tooling downloads/verifies its configured toolchain and dependencies. Android uses the pinned JDK 17, Gradle 9.3.1, SDK and NDK settings declared by `tools/build.py` and the Gradle project. Private signing material is deliberately absent; a new build needs an explicitly supplied signing setup to retain the published APK's identity.

From an extracted project root:

```bash
python tools/build.py server --cache work/build-cache --expect-revision transport-rework3-rev7
python tools/build.py android --cache work/build-cache --expect-revision transport-rework3-rev7 --accept-android-licenses
python -m unittest discover -s tools -p 'test_*.py'
python tools/field_qualification.py --go /path/to/go --repeats 1 --results work/field
python tools/transport_qualification.py --go /path/to/go --accounts 15 --streams both --repeats 1 --uid-history 64 --stability-only --results work/fresh
python tools/transport_qualification.py --go /path/to/go --accounts 15 --streams both --repeats 1 --uid-history 64 --retained-state --stability-only --results work/retained
```

The source ZIP is also executable with Python, validates its manifest, extracts a fresh tree and accepts the same server/Android build targets. `tools/transport_qualification.py` rejects skipped or incomplete required cases, changed source and race-instrumented speed measurements. Without `--stability-only`, the separate historical 90 Mbit/s policy applies. The core build explicitly enables the Rev7 stability policy and mandatory outage scenarios.

Run race checks separately from timed load measurements. The release-driver snapshot at `archives/rev7/release-drivers/validate_candidate.py` records the exact package selections and all nine final commands. Its workspace/cache paths are this release environment's paths; adapt them to a new workspace before use. Run stages serially. This qualification host had a two-core CPU quota; concurrent compression, builds or trace analysis can invalidate timing comparisons. Do not reduce per-scenario recovery or probe deadlines to make a run fit the package timeout.

## Install and operate

Update **both endpoints** to Rev7 and preserve server configuration, journals, wallet state and client profiles. Choose the AMD64 `.bin` for x86-64 Linux or ARM64 `.bin` for ARM Linux, keep the current state directory, and restore executable permission if necessary. The `version` command must report `transport-rework3-rev7`.

The APK has application ID `org.itom.client.dev.rev7`, version code 9207 and the same certificate as the published Rev6 APK. Install it as an update so profiles and wallet history remain. The certificate SHA-256 is `39667cd3f91521b09af012b2ca08f8f0ba0d7e07f14b32a9a1e794cb3f4bf999`; `SIGNER.txt` records APK verification. Private keys and signing caches are not included.

An ordinary update does not need an account reset. When intentionally starting over, stop the service and use server manager **3. Mail accounts → 6. Reset accounts database**, then type `RESET ACCOUNTS`. This removes active logical credentials and retired account definitions and creates a fresh account-ID namespace. It preserves client/server identity, provider mail and physical accounting files. Re-add the provider credentials and export replacement profiles to **every client** before restarting. This operation is CLI-only; it is neither a mailbox purge nor forensic disk erase.

Formatted global and per-client messages remain in manager menu 17 and the `message` CLI. Individual messages override the global message; saving or clearing the global message clears all individual entries. Minecraft-style `&0`–`&f`, bold/italic/underline/strikethrough/reset and escaped `&&` remain supported by Android. Settings' introduction banner stays removed. Existing dark UI and admin settings remain; full control mappings are in `docs/rev5/ADMIN.md`.

For a new failure, capture both endpoints immediately with overlapping clocks and include the first failed application attempt, the load interval and at least a minute afterward. Keep raw captures and profiles private. Distinguish control-socket repair from a logical session replacement, target connection failure and application-level Speedtest rejection. Do not reset accounting or remove provider mail as a diagnostic shortcut.

## Remaining work and boundaries

The release is internally stability-qualified. Physical phone, live Rambler and an actual systemd-host recovery cycle were not available in this environment; the native supervisor restart and durable rollback are tested locally. The earlier whole-app mobile crash has no verified root cause, and the original first Speedtest failure and external EOF trigger cannot be reconstructed from the retained window. These limits are recorded rather than presented as fixed field observations.

The next field pass should check cold startup, several SINGLE/MULTI tests back-to-back, an existing Telegram/chat connection and a fresh connection after load, extended idle, and a Wi-Fi/mobile transition, using matching Rev7 endpoints. Throughput improvement is subsequent work. Start from the bounded eight-worker state and the recorded fresh/retained results; investigate useful BODY occupancy, per-account service time and prefix deferrals before raising parallelism. Keep all stability/accounting gates mandatory when resuming performance work.

No raw user captures, account passwords, profile secrets, signing keys or build caches are included in Git or release assets.
'''
(ROOT/'HANDOFF.md').write_text(text)
print('Full project handoff prepared from completed same-source qualification')
