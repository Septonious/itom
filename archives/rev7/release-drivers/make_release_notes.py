from pathlib import Path
import json
w=Path('/workspace/work/rev7');q=json.loads((w/'source/itom/docs/rev7/evidence/rev7_validation.json').read_text())
assert q['all_internal_gates_passed'] and len(q['pipeline_steps'])==9
rows=[]
for matrix,key in [('Fresh','speed_cases'),('Retained','retained_speed_cases')]:
 for c in q[key]:
  a=c['supervised_acceptance'];r=c['supervised_result'];rows.append(f"| {matrix} {c['name']} | {float(a['minimum_verified_mbps']):.2f} | {int(a['max_rtt_ns'])/1e6:.1f} | {r['overlap']}/30 |")
text=f'''# ITOM 0.9.2 Rev7

Rev7 repairs the account-wide reconnect cascade following a physical control socket failure. The control connection is replaced while preserving its encrypted carrier and existing TCP streams. Uncertain APPENDs keep their charged physical debt; replacement attempts use fresh counters and independent charges. Required control capacity is reserved at both admission levels, and control/receive repair and restoration of the first usable DATA writer can bypass queued optional LOGINs. Sole DATA receiver repair is also prioritized; additional writers remain optional. Socket-capped mixed layouts are covered too.

A missing handshake reply can now be recovered by repeating the exact authenticated hello and returning its bounded cached reply. Replays cannot reset the session; altered/expired/excess offers are rejected. Active traffic gets a temporary shorter audit after repeated actual missed IDLE notifications, while quiet-account pacing remains. Carrier health traces now populate the stream count.

Retained-state stability is an explicit release gate. Recovery uses several complete bulk copies per lease where the actual ledger allows, and reserves upload refill capacity against download competition. Old grants, physical byte/message caps, local latency reserves, and existing proof requirements remain. Origin-balance reads reuse only exact unchanged database rows and the current transaction snapshot; no cache can authorize admission or refund.

Bulk flight admission follows collection order before parallel physical work. Ready unsent predecessors take priority over speculative future offsets; younger records wait only within their own stream. Definitive pre-publication deferrals return immediately instead of waiting for a sibling APPEND. Both bulk queues share the original bounded handoff budget. Remaining ordinal capacity is balanced between directions; whole-lease byte alignment and earlier bounded cleanup keep small retained windows usable. A full journal no longer wakes its own blocked writer without a real capacity change. The original eight-worker bound rotates across the account pool; raising speculative concurrency did not improve the sustained gate and was reverted.

All **nine** final internal gates passed on source `{q['qualification_source_sha256']}`:

- {q['core']['passed_test_events']} core test events, zero failures/skips; IMAP-fork tests/vet passed; Linux AMD64 and ARM64 built.
- The complete **48-case** field-derived application suite in the mandatory core gate, with real fsync. Extra suite repetitions were removed at the user’s request. Cases include simultaneous socket failures and slow optional logins, lost handshake/APPEND completions, the original chat socket across repeated transfers, retained quotas through 188/192 slots, missing IDLE notifications, and fifteen accounts idling for 110 seconds before the next exchange.
- Accounting/recovery/native admin race gates passed. Existing server-authoritative admin roles and the independent sixty-second rollback watchdog remain; irreversible operations stay CLI-only.
- {q['android_domain_tests']['tests']} Android domain tests and {q['android_callback_adapter_tests']['tests']} app tests passed; lint {q['android_lint']['errors']} errors/{q['android_lint']['warnings']} warnings; both native Android ABIs passed.
- **Four independent load and post-load stability runs:** one SINGLE and one 24-stream-per-direction MULTI on fresh state, repeated with **6 MiB and 130 old quota slots retained per account**. Each uses fifteen accounts, sparse physical UID history, ten-second warmup and sixty-second measurement. The user waived the 90 Mbit/s floor for this release; measured speeds are retained below. Every one-second window must carry useful traffic in both directions. Every loaded RTT must stay below 1,100 ms, and all thirty probes must overlap duplex traffic with zero misses/timeouts. Original TCP connections, logical sessions and healthy carrier epochs must survive. After load, six exchanges on the original socket across idle gaps, a fresh verified 256 KiB transfer, and another original-socket exchange must succeed without restarting either endpoint. The RTT limit retains the requested ten-percent margin. No throughput certification is claimed.

| Run | Lowest recorded full/tail Mbit/s | Maximum loaded RTT ms | Duplex probes |
| --- | ---: | ---: | ---: |
{chr(10).join(rows)}

Update **both app and server**. The APK keeps the published Rev6 application ID `org.itom.client.dev.rev7` and signing certificate, with version code 9207. Install it as an update and preserve app/server state and profiles. Choose AMD64 for x86-64 Linux or ARM64 for ARM Linux; restore executable permission if required. The server `version` reports `transport-rework3-rev7`. Verify assets against `SHA256SUMS`.

The accounts database reset remains available in server manager **3. Mail accounts → 6. Reset accounts database**, with the service stopped and typed confirmation. Reset clears logical account credentials and retired definitions, assigns a fresh namespace, and requires exporting replacement profiles to every client. It preserves server/client identities, provider mail and accounting files; it is CLI-only. No reset is needed for an ordinary Rev7 update.

This is an internally qualified **field-validation prerelease**. The original first failed Speedtest and the remote trigger for the correlated EOFs are outside paired trace retention. The laboratory results do not guarantee live-provider speed or establish the cause of the earlier whole-app mobile crash. Physical phone, live Rambler and actual systemd-host validation remain separate. See the attached analysis and exact qualification evidence.

GitHub Actions assembles the tested assets from hashed parts, verifies source identity and all nine gates including both speed matrices, and verifies every uploaded asset size and SHA-256 before publication. Private captures, credentials, signing keys and build caches are excluded. The source archive, analysis, checksums evidence and a full `HANDOFF.md` are attached. The handoff covers architecture, repairs, invariants, validation commands, provenance and follow-up work.
'''
p=Path('/workspace/work/rev5/publication/builds/0.9.2-Rev7/RELEASE_NOTES.md');p.parent.mkdir(parents=True,exist_ok=True);p.write_text(text)
print('Rev7 release notes prepared from exact qualification')
