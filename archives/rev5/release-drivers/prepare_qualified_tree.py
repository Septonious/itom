from pathlib import Path
import json, shutil, sys
w=Path('/workspace/work/rev5'); s=w/'source/itom'; r=w/'publication'
sys.path.insert(0,str(s/'tools'))
from transport_qualification import source_fingerprint
q=json.loads((s/'docs/rev5/evidence/rev5_validation.json').read_text())
assert q['all_internal_gates_passed'] and len(q['pipeline_steps'])==10
assert source_fingerprint()==q['qualification_source_sha256']
excluded={'.git','.gradle','.kotlin','__pycache__','build','dist','libs','reports','.idea'}
def ignore(directory,names):
 return [n for n in names if n in excluded or n=='local.properties' or n.endswith(('.pyc','.pyo','.test','.jks','.keystore'))]
mirror=r/'archives/rev5/source'
shutil.rmtree(mirror)
shutil.copytree(s,mirror,ignore=ignore)
(r/'README.md').write_text('''# ITOM

ITOM transports TCP application streams through authenticated IMAP mailboxes. This branch contains **Rev5, internally qualified and packaged for field validation**.

- [Download APK and Linux binaries](https://github.com/Septonious/itom/releases/tag/v0.9.2-Rev5)
- [Release notes and installation](builds/0.9.2-Rev5/RELEASE_NOTES.md)
- [Rev5 source](archives/rev5/source)
- [Field analysis and repair design](archives/rev5/source/docs/rev5/REV5_ANALYSIS_2026-10-15.md)
- [Final qualification evidence](archives/rev5/source/docs/rev5/evidence/rev5_validation.json)
- [Android server admin setup and rollback behavior](archives/rev5/source/docs/rev5/ADMIN.md)

Rev5 repairs retained-mail quota starvation, bounds paced BODY reads and unread downstream queues, uses authenticated peer-delivery feedback to size envelopes, and restores DATA writers during recovery. It preserves durable charges and cleanup proof requirements. A per-sweep proof cache removes repeated decoding without weakening those checks.

The Android app has a darker, higher-contrast theme with vivid accents. Server messages support global text, per-client overrides and Minecraft-style formatting; every global update clears individual overrides. Settings exposes the IMAP server-admin console for an enabled server-side client named `admin`, authenticated with its private key. Reversible mutations have an independent durable watchdog: 60 seconds without recovery rolls back; 60 continuous healthy seconds commits. Irreversible operations remain CLI-only.

All ten internal gates passed on the exact packaged source: Android and native builds/tests, lint, full core/IMAP tests, four 32-case field suites, accounting/recovery/admin race checks, and all six SINGLE/MULTI performance runs. Each speed run independently met the 90 Mbit/s useful-throughput floor in both directions across all three windows, every loaded RTT below 1,100 ms, and 30/30 overlapping duplex probes without misses or timeouts. The final evidence records exact results and source identity.

This is a **prerelease**, pending physical-phone, live-provider and actual systemd-host validation. The user confirmed that a router reset fixed the earlier Wi-Fi bottleneck. The exact regular Speedtest-app error and earlier whole-app mobile crash remain unconfirmed without matching diagnostics; laboratory passes do not establish that those field symptoms are resolved.

Update both app and server, preserving profiles and state. GitHub Actions reassembles hashed release parts, verifies source identity and all gate evidence, then verifies every uploaded asset size and SHA-256 before publication. Private captures, credentials, caches and signing keys are excluded.

Historical Rev2/Rev3/Rev4 archives and the earlier unstable Rev5 recovery checkpoint remain available with their original evidence.
''')
p=r/'archives/rev5/checkpoint/README.md'
p.write_text(p.read_text()+'''\n## Historical snapshot\n\nThis directory records development before final qualification. Its status, handoff and source-file manifest describe checkpoint commit `fdce4298ac3c444ed2c224966d46d40cd196f700`. The sibling `../source/` now contains the final qualified source; use its `docs/rev5/evidence/rev5_validation.json` and the release manifest for final results. Historical failures above are retained and are not waived or counted as final passes.\n''')
d=r/'archives/rev5/release-drivers';d.mkdir(exist_ok=True)
for name in ('validate_candidate.py','prepare_validation_summary.py','make_release_notes.py','package_release_assets.py','prepare_qualified_tree.py'):
 shutil.copy2(w/name,d/name)
print('Qualified source and release documentation mirrored')
