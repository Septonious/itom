"""Verify exact-source ITOM 1.1.2 artifacts and restore the previously signed APK."""
from pathlib import Path
import hashlib,json,subprocess,zipfile
from validate_models import validate
pub=Path(__file__).resolve().parent;repo=pub.parents[3];root=Path('release-assets')
def read(p):return json.loads(p.read_text())
def digest(p):return hashlib.sha256(p.read_bytes()).hexdigest()
m=read(pub/'ITOM_112_BUILD.json');validation=read(pub/'ARTIFACT_VALIDATION.json')
source='06decfae354ec95a344005751f5091cec55a5ff5';revision='itom-1-1-2-final'
assert m['source_commit']==validation['source_commit']==source
assert not m['artifact_validation_pending'] and validation['exit_code']==0
assert m['checks']==validation['checks'] and all(m['checks'].values())
assert m['field_ready_baseline']=='MAILSTREAM-RC18' and not m['field_qualified'] and not m['real_provider_tested']
assert (root/'SOURCE_COMMIT').read_text().strip()==source
subprocess.run(['sha256sum','--check','NATIVE_SOURCE_SHA256SUMS'],cwd=root,check=True)
for arch,value in m['server_sha256'].items():assert digest(root/f'ITOM-1.1.2-linux-{arch}.bin')==value
parts=read(Path('android-evidence/manifest.json'));report=read(Path('android-evidence/build-report.json'))
assert parts['source_commit']==source and report['source_revision']==revision and report['status']=='passed'
assert report['apk_identity']==m['apk_identity']=={'application_id':'org.itom.client','version_code':10147,'version_name':'1.1.2'}
for k in ('domain_tests','app_unit_tests'):
 d=report[k];assert d['tests']>0 and d['failures']==d['errors']==d['skipped']==0
assert report['lint']['errors']==0 and report['android_variant']=='release'
assert 'app:testReleaseUnitTest' in report['checks'] and 'app:assembleRelease' in report['checks']
chunks=[]
for part in parts['parts']:
 raw=Path('apk-parts',part['name']).read_bytes();assert len(raw)==part['bytes'] and hashlib.sha256(raw).hexdigest()==part['sha256'];chunks.append(raw)
original=Path('original.apk');original.write_bytes(b''.join(chunks))
assert digest(original)==parts['apk_sha256']==report['sha256']==m['source_ci_apk_sha256']
signed=root/'ITOM-1.1.2.apk'
subprocess.run(['python3',str(pub/'restore_signed_apk.py'),str(original),str(signed)],check=True)
signer=subprocess.check_output(['java','-jar','signing-tools/apksigner.jar','verify','--verbose','--print-certs',str(signed)],text=True)
assert 'Signer #1 certificate SHA-256 digest: '+m['signer_certificate_sha256'] in signer
assert 'Verified using v3 scheme (APK Signature Scheme v3): true' in signer
(root/'SIGNER.txt').write_text(signer)
assert digest(signed)==m['signed_apk_sha256'] and signed.stat().st_size==m['signed_apk_bytes']
with zipfile.ZipFile(signed)as z:
 for name,value in report['apk_native_libraries'].items():
  raw=z.read(name);assert hashlib.sha256(raw).hexdigest()==value==m['apk_native_libraries'][name]
  assert revision.encode()in raw and b'itom.local/mailstream'in raw and b'bulk_pacing_deferrals'in raw
  assert all(key in raw for key in (b'TransportStreams',b'RateReady',b'AccountID',b'OpeningTCP'))
manifest=read(pub/'ITOM_112_SOURCE_MANIFEST.json');assert manifest['source_commit']==source
for name,value in manifest['files'].items():assert digest(Path(name))==value,name
safetyfiles=list(Path('evidence/hosted-safety').rglob('core-report.json'));assert len(safetyfiles)==1
safety=read(safetyfiles[0]);assert safety['status']=='passed' and safety['source_revision']==revision
for k in ('mailstream_normal_tests','mailstream_race_tests','native_normal_tests','native_race_tests','imap_fork_tests','vet'):assert safety[k]=='passed'
models,regressions,comparison=validate(Path('evidence'),repo)
assert models==m['models'] and regressions==m['regressions'] and comparison==m['accepted_baseline_comparison']
for name in ('ITOM_112_BUILD.json','ITOM_112_SOURCE_MANIFEST.json','ARTIFACT_VALIDATION.json','ITOM-1.1.2-evidence.zip'):(root/name).write_bytes((pub/name).read_bytes())
(root/'ANDROID_BUILD.json').write_bytes(Path('android-evidence/build-report.json').read_bytes())
(root/'RELEASE_NOTES.md').write_bytes((repo/'archives/1.1.2/RELEASE_NOTES.md').read_bytes())
(root/'SHA256SUMS').write_text(''.join(digest(p)+'  '+p.name+'\n'for p in sorted(root.iterdir())if p.is_file()and p.name!='SHA256SUMS'))
expected=read(pub/'EXPECTED_EXPORTED_ASSETS.json')
actual={p.name:{'sha256':digest(p),'bytes':p.stat().st_size}for p in root.iterdir()if p.is_file()}
assert actual==expected
print('Verified all 14 ITOM 1.1.2 files, signed APK, JNI, exact source and matched transport models')
