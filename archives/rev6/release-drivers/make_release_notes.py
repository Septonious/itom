from pathlib import Path
import json
w=Path('/workspace/work/rev6');q=json.loads((w/'source/itom/docs/rev6/evidence/rev6_validation.json').read_text())
assert q['all_internal_gates_passed'] and len(q['pipeline_steps'])==10
rows=[]
for c in q['speed_cases']:
 a=c['supervised_acceptance'];r=c['supervised_result'];rows.append(f"| {c['name']} | {float(a['minimum_verified_mbps']):.2f} | {int(a['max_rtt_ns'])/1e6:.1f} | {r['overlap']}/30 |")
text=f'''# ITOM 0.9.2 Rev6

Rev6 repairs slow startup when additional IMAP sockets stall and adds bounded keepalives for idle sockets. Automatic layout setup has a two-second optional budget; auxiliary activation shares four seconds. The authenticated base connection can activate and carry traffic while optional DATA capacity repairs on the parent lifetime. Explicit split layout still requires its actual independent receiver.

Idle keepalives use NOOP only. They skip busy sockets, never wait behind application commands, and never count as authenticated peer progress or quota-release evidence. Provider LOGIN failures now retain safe stage/reason and whitelisted standard response codes. The settings introduction banner shown in the report has been removed; the settings controls remain.

**Reset accounts:** stop the server service, open server manager **3. Mail accounts → 6. Reset accounts database**, and type `RESET ACCOUNTS`. This clears every stored mail login/password and retired account definition from the logical configuration. Server/client identities, provider messages and accounting files remain. A fresh random namespace prevents new accounts inheriting old journal IDs. Re-add provider accounts and **export replacement profiles to every client before restarting**. Reset configurations use server schema 6; older server versions cannot read them. Reset is CLI-only and is not a mailbox purge or forensic disk erase.

The new field capture includes accounts that first reject LOGIN and later accept the same saved credentials. The precise provider-side reason is unconfirmed; resetting a database does not itself resolve a provider restriction. No live Rambler test or physical phone test is claimed.

All ten internal gates passed on source `{q['qualification_source_sha256']}`:

- {q['core']['passed_test_events']} core test events, with zero failures/skips; IMAP-fork tests and vet passed; Linux AMD64/ARM64 built.
- Four complete **34-case** field repetitions, including one with 12 ms added server commit delay and real fsync. Each includes fifteen accounts idling for **110 seconds**, a provider fixture expiring unused sockets after 45 seconds, unchanged carrier generations, no idle socket expirations and a successful next TCP exchange. The suite also covers half the accounts rejecting login while extras stall, retained quota, outages, HTTP phases, canceled transfers and chat under bulk load.
- Accounting/admin, recovery and native admin reconnect race gates passed.
- {q['android_domain_tests']['tests']} Android domain tests and {q['android_callback_adapter_tests']['tests']} app tests passed; lint {q['android_lint']['errors']} errors/{q['android_lint']['warnings']} warnings; native checks and both Android ABIs passed.
- Three SINGLE and three 24-stream-per-direction MULTI cases passed independently, with fifteen accounts and retained physical UID history. Both useful directions reach 90 Mbit/s across the full interval, last thirty seconds and last ten seconds. Every loaded RTT is below 1,100 ms, with 30/30 overlapping duplex probes, no misses and no timeouts.

| Run | Lowest verified useful Mbit/s | Maximum loaded RTT ms | Duplex probes |
| --- | ---: | ---: | ---: |
{chr(10).join(rows)}

Update **both** app and server. Install the APK over the existing app (`org.itom.client.dev.rev7`, version code 9206, same signing certificate). Preserve existing state unless you explicitly choose the accounts reset; after reset, replace every client profile. Choose the AMD64 binary for x86-64 Linux or ARM64 for ARM Linux and restore executable permission if needed. `version` reports `transport-rework3-rev6`. Verify downloads against `SHA256SUMS`.

This is an internally qualified prerelease. Provider policy, the complete cause of idle peer silence in the field, the prior Speedtest-app error and earlier whole-app mobile crash remain unconfirmed without matching server/provider/device diagnostics. Actual systemd-host rollback remains a field-validation requirement. Existing formatted server messages, dark UI and guarded IMAP admin controls are retained.

GitHub Actions reassembles the tested assets, verifies source identity and all gate evidence, and verifies every uploaded asset size and SHA-256 before publishing. Private captures, credentials, build caches and signing keys are excluded. Sources, analysis, checksums and qualification evidence are attached.
'''
p=Path('/workspace/work/rev5/publication/builds/0.9.2-Rev6/RELEASE_NOTES.md');p.parent.mkdir(parents=True,exist_ok=True);p.write_text(text)
print('Rev6 release notes prepared from final qualification')
