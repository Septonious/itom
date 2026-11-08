"""Restore already-verified public APK bytes and publish downloadable test files.

No signing key or recovery key is present in this script or its input tree.
"""
import base64
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import shutil
import struct
import subprocess
import zipfile

SOURCE = '0393b34b281699861b77da247066118dae807fc5'
REVISION = 'itom-1-1-5-final'
CERT = '0fdd51e00489c93b13ed5ce35e13257a9c8964f2f93b1660e5e542cfbf6fb4f0'
TAG = 'v1.1.5-testing'
PUBLIC = Path('testing/1.1.5/downloads')
ROOT = Path('test-release-assets')
ROOT.mkdir(exist_ok=False)


def read(path):
    return json.loads(path.read_text())


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def run(*args):
    return subprocess.check_output(args, text=True, stderr=subprocess.STDOUT)


expected = read(Path('testing/1.1.5/TESTING_BUILD.json'))
assert expected['source_commit'] == SOURCE and expected['testing_only']
assert expected['new_signer_user_authorized'] and expected['signer_certificate_sha256'] == CERT
assert Path('linux-input/SOURCE_COMMIT').read_text().strip() == SOURCE
for arch, machine in (('amd64', 62), ('arm64', 183)):
    name = f'ITOM-1.1.5-linux-{arch}.bin'
    source = Path('linux-input')/name
    raw = source.read_bytes()
    assert raw[:6] == b'\x7fELF\x02\x01' and struct.unpack_from('<H', raw, 18)[0] == machine
    assert digest(source) == expected['files'][name]['sha256']
    assert len(raw) == expected['files'][name]['bytes']
    shutil.copy2(source, ROOT/name)
(ROOT/'ITOM-1.1.5-linux-amd64.bin').chmod(0o755)
assert REVISION in run(str((ROOT/'ITOM-1.1.5-linux-amd64.bin').resolve()), 'version')

manifest = read(Path('android-input/manifest.json'))
report = read(Path('android-input/build-report.json'))
assert manifest['source_commit'] == SOURCE
assert report['status'] == 'passed' and report['source_revision'] == REVISION
assert report['apk_identity'] == {'application_id': 'org.itom.client', 'version_code': 10150, 'version_name': '1.1.5'}
assert report['android_variant'] == 'release' and report['lint']['errors'] == 0
for name in ('domain_tests', 'app_unit_tests'):
    summary = report[name]
    assert summary['tests'] > 0 and summary['failures'] == summary['errors'] == summary['skipped'] == 0
parts = []
for part in manifest['parts']:
    raw = (Path('apk-parts')/part['name']).read_bytes()
    assert len(raw) == part['bytes'] and hashlib.sha256(raw).hexdigest() == part['sha256']
    parts.append(raw)
original = b''.join(parts)
assert len(original) == manifest['apk_bytes']
assert hashlib.sha256(original).hexdigest() == manifest['apk_sha256'] == report['sha256']
patch = json.loads(gzip.decompress(base64.b64decode((PUBLIC/'public-apk.patch.b64').read_text().strip(), validate=True)))
assert hashlib.sha256(original).hexdigest() == patch['source_sha256']
chunks = []
for operation in patch['operations']:
    if 'copy' in operation:
        offset, length = operation['copy']
        assert 0 <= offset <= offset+length <= len(original)
        chunks.append(original[offset:offset+length])
    else:
        chunks.append(base64.b64decode(operation['literal'], validate=True))
signed = b''.join(chunks)
name = 'ITOM-1.1.5.apk'
assert len(signed) == patch['result_bytes'] == expected['files'][name]['bytes']
assert hashlib.sha256(signed).hexdigest() == patch['result_sha256'] == expected['files'][name]['sha256']
apk = ROOT/name
apk.write_bytes(signed)
with zipfile.ZipFile(io.BytesIO(original)) as a, zipfile.ZipFile(apk) as b:
    assert a.namelist() == b.namelist()
    for entry in a.namelist():
        assert hashlib.sha256(a.read(entry)).digest() == hashlib.sha256(b.read(entry)).digest(), entry
    for entry, value in report['apk_native_libraries'].items():
        raw = b.read(entry)
        assert hashlib.sha256(raw).hexdigest() == value and REVISION.encode() in raw and b'TCPSendErrors' in raw
jar = 'signing-tools/apksigner.jar'
verify = run('java', '-jar', jar, 'verify', '--verbose', '--print-certs', str(apk))
verify_v2 = run('java', '-jar', jar, 'verify', '--verbose', '--print-certs', '--min-sdk-version', '24', '--max-sdk-version', '27', str(apk))
assert 'Signer #1 certificate SHA-256 digest: '+CERT in verify
assert 'Signer #1 certificate SHA-256 digest: '+CERT in verify_v2
assert 'Verified using v3 scheme (APK Signature Scheme v3): true' in verify
assert 'Verified using v2 scheme (APK Signature Scheme v2): true' in verify_v2
(ROOT/'SIGNER.txt').write_text(verify+'\nCompatibility signature verification:\n'+verify_v2)
(ROOT/'SOURCE_COMMIT').write_text(SOURCE+'\n')
(ROOT/'ANDROID_BUILD.json').write_text(json.dumps(report, indent=2)+'\n')
(ROOT/'TESTING_STATUS.json').write_text(json.dumps(expected, indent=2)+'\n')
shutil.copy2(PUBLIC/'README-TESTING.txt', ROOT/'README-TESTING.txt')

def sums(paths):
    return ''.join(digest(p)+'  '+p.name+'\n' for p in sorted(paths))

# The testing bundle contains software and public verification only.
inner = list(ROOT.iterdir())
(ROOT/'SHA256SUMS').write_text(sums(inner))
with zipfile.ZipFile(ROOT/'ITOM-1.1.5-testing.zip', 'w', compression=zipfile.ZIP_DEFLATED, compresslevel=6) as z:
    for path in sorted(inner+[ROOT/'SHA256SUMS']):
        z.write(path, path.name)
with zipfile.ZipFile(ROOT/'ITOM-1.1.5-testing.zip') as z:
    assert z.testzip() is None
    assert not any(n.endswith(('.jks', '.p12', '.pk8')) or n == 'signing.json' for n in z.namelist())

(ROOT/'SHA256SUMS').write_text(sums(p for p in ROOT.iterdir() if p.name != 'SHA256SUMS'))
assets = {p.name: {'bytes': p.stat().st_size, 'sha256': digest(p)} for p in ROOT.iterdir()}
Path('EXPECTED_TEST_ASSETS.json').write_text(json.dumps(assets, indent=2)+'\n')
print('Prepared exact signed APK, both server binaries, and testing bundle.')
