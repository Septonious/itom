"""Promote the exact field-accepted public software bytes; no build or signing."""
import hashlib,json,shutil,struct,zipfile
from pathlib import Path

base=Path('releases/1.1.5')
status=json.loads((base/'RELEASE_STATUS.json').read_text())
assert status['release_channel']=='stable' and status['transport_status']=='complete'
assert status['accepted_by']=='user' and status['tested_artifacts_preserved_exactly']
source=status['source_commit']
assert Path('tested/SOURCE_COMMIT').read_text().strip()==source
assert Path('source-input/SOURCE_COMMIT').read_text().strip()==source
report=json.loads(Path('tested/ANDROID_BUILD.json').read_text())
assert report['status']=='passed' and report['source_revision']=='itom-1-1-5-final'
assert report['apk_identity']=={'application_id':'org.itom.client','version_code':10150,'version_name':'1.1.5'}
assert report['lint']['errors']==0
assert sum(report[n]['tests']for n in('domain_tests','app_unit_tests'))==102
assert status['signer_certificate_sha256'] in Path('tested/SIGNER.txt').read_text()
root=Path('stable-assets');root.mkdir(exist_ok=False)
for name,expected in status['files'].items():
    p=Path('source-input'if name.endswith('-sources.zip')else'tested')/name
    raw=p.read_bytes()
    assert len(raw)==expected['bytes'] and hashlib.sha256(raw).hexdigest()==expected['sha256'],name
    if name.endswith('.bin'):
        assert raw[:6]==b'\x7fELF\x02\x01'
        assert struct.unpack_from('<H',raw,18)[0]==(62 if 'amd64' in name else 183)
    else:
        with zipfile.ZipFile(p)as z:assert z.testzip() is None
    shutil.copy2(p,root/name)
for name in('SOURCE_COMMIT','SIGNER.txt','ANDROID_BUILD.json'):shutil.copy2(Path('tested')/name,root/name)
for name in('RELEASE_STATUS.json','RELEASE_NOTES.md'):shutil.copy2(base/name,root/name)
def sums(paths):
    return ''.join(hashlib.sha256(p.read_bytes()).hexdigest()+'  '+p.name+'\n'for p in sorted(paths))
inner=list(root.iterdir());(root/'SHA256SUMS').write_text(sums(inner))
with zipfile.ZipFile(root/'ITOM-1.1.5.zip','w',compression=zipfile.ZIP_DEFLATED,compresslevel=6)as z:
    for p in sorted(inner+[root/'SHA256SUMS']):z.write(p,p.name)
with zipfile.ZipFile(root/'ITOM-1.1.5.zip')as z:
    assert z.testzip() is None
    assert not any(n.endswith(('.jks','.p12','.pk8'))or n=='signing.json'for n in z.namelist())
(root/'SHA256SUMS').write_text(sums(p for p in root.iterdir()if p.name!='SHA256SUMS'))
expected={p.name:{'bytes':p.stat().st_size,'sha256':hashlib.sha256(p.read_bytes()).hexdigest()}for p in root.iterdir()}
Path('EXPECTED_STABLE_ASSETS.json').write_text(json.dumps(expected,indent=2)+'\n')
print('Stable assets prepared; APK, both binaries and source archive exactly match qualified bytes.')
