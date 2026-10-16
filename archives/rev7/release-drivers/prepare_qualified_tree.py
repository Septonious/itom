from pathlib import Path
import json,shutil,sys
w=Path('/workspace/work/rev7');s=w/'source/itom';r=Path('/workspace/work/rev5/publication')
sys.path.insert(0,str(s/'tools'));from transport_qualification import source_fingerprint
q=json.loads((s/'docs/rev7/evidence/rev7_validation.json').read_text())
assert q['all_internal_gates_passed'] and len(q['pipeline_steps'])==11 and len(q['retained_speed_cases'])==6
assert source_fingerprint()==q['qualification_source_sha256']
excluded={'.git','.gradle','.kotlin','__pycache__','build','dist','libs','reports','.idea'}
def ignore(d,names):return[n for n in names if n in excluded or n=='local.properties' or n.endswith(('.pyc','.pyo','.test','.jks','.keystore'))]
mirror=r/'archives/rev7/source';shutil.rmtree(mirror);shutil.copytree(s,mirror,ignore=ignore)
(r/'README.md').write_text('''# ITOM

Rev7 is internally qualified and packaged for field validation. It repairs physical-control failure cascades, critical LOGIN admission, missing handshake replies, notification fallback and retained-state duplex capacity.

- [APK and Linux binaries](https://github.com/Septonious/itom/releases/tag/v0.9.2-Rev7)
- [Release notes and installation](builds/0.9.2-Rev7/RELEASE_NOTES.md)
- [Rev7 source](archives/rev7/source)
- [Paired-capture analysis](archives/rev7/source/docs/rev7/REV7_ANALYSIS_2026-10-16.md)
- [Exact qualification evidence](archives/rev7/source/docs/rev7/evidence/rev7_validation.json)

All eleven internal gates passed: complete core/IMAP/vet, Android/native tests and lint, four 48-case field-derived repetitions, race checks, and twelve SINGLE/MULTI runs across both fresh and retained quota state. Each performance run independently applies the requested ten-percent tolerance: both useful directions at least 90 Mbit/s, all loaded RTTs below 1,100 ms, all thirty probes overlapping duplex traffic, zero misses/timeouts.

Update app and server together and preserve profiles/state. The explicit CLI accounts reset remains available when starting over. Existing formatted server messages, dark UI and guarded IMAP admin controls remain. Irreversible operations remain CLI-only.

This is a field-validation prerelease: laboratory results are separate from live-provider/device measurements. The original failed initial Speedtest and external cause of the correlated EOFs are not established by the retained captures. GitHub Actions assembles exact tested assets and verifies remote digests before publishing. Raw captures and signing material are excluded. Historical revisions preserve their original results.
''')
d=r/'archives/rev7/release-drivers';d.mkdir(exist_ok=True)
for n in ('validate_candidate.py','prepare_validation_summary.py','package_release_assets.py','make_release_notes.py','prepare_qualified_tree.py'):shutil.copy2(w/n,d/n)
print('Qualified Rev7 source and documentation mirrored')
