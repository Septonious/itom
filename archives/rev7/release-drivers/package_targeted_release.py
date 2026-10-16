"""Package the exact internally qualified Rev7 bits for a field-validation prerelease."""
from pathlib import Path
import datetime
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import zipfile

WORK = Path('/workspace/work/rev7')
SOURCE = WORK / 'source/itom'
REPO = Path('/workspace/work/rev5/publication')
STAGE = WORK / 'release-assets'
ARCHIVE = REPO / 'archives/rev7'
PREFIX = 'ITOM-0.9.2-Rev7'
TAG = 'v0.9.2-Rev7'
CACHE = Path('/workspace/work/rev3/build-cache')
sys.path.insert(0, str(SOURCE / 'tools'))
from build_core import runtime_hash
from package_sources import package
from transport_qualification import source_fingerprint


def read(path):
    return json.loads(path.read_text())


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def entry(path):
    return dict(name=path.name, size=path.stat().st_size, sha256=digest(path))


qualification = read(SOURCE / 'docs/rev7/evidence/rev7_validation.json')
android = read(SOURCE / 'dist/build-report.json')
identity = source_fingerprint()
assert qualification['classification']=='REV7_TARGETED_VALIDATION_PRERELEASE'
assert qualification['all_required_targeted_checks_passed'] is True
assert qualification['all_internal_gates_passed'] is False and qualification['final_full_suite_completed'] is False
assert identity==qualification['qualification_source_sha256']
assert runtime_hash(SOURCE/'core')==qualification['first_party_core_runtime_sha256']
assert android['status']=='passed'
core={'core_runtime_sha256':qualification['first_party_core_runtime_sha256']}

STAGE.mkdir(exist_ok=False)
ARCHIVE.mkdir(parents=True,exist_ok=True)
assert not (ARCHIVE/'manifest.json').exists()
apk = SOURCE / 'dist/ITOM-0.9.2-Rev7-debug.apk'
assert digest(apk) == android['sha256']
jdk = next((CACHE/'jdk17').rglob('bin/java')).parents[1]
env = os.environ.copy()
env['JAVA_HOME'] = str(jdk)
env['PATH'] = str(jdk/'bin') + ':' + env['PATH']
signer = subprocess.run([str(CACHE/'android-sdk/build-tools/36.0.0/apksigner'),
                         'verify', '--verbose', '--print-certs', str(apk)],
                        env=env, capture_output=True, text=True, check=True)
certificate = re.search(r'certificate SHA-256 digest: ([0-9a-f]+)', signer.stdout).group(1)
assert certificate == '39667cd3f91521b09af012b2ca08f8f0ba0d7e07f14b32a9a1e794cb3f4bf999'
assert 'Verified using v2 scheme (APK Signature Scheme v2): true' in signer.stdout
with zipfile.ZipFile(apk) as z:
    assert z.testzip() is None
    for name, expected in android['apk_native_libraries'].items():
        assert hashlib.sha256(z.read(name)).hexdigest() == expected
shutil.copy2(apk, STAGE/(PREFIX+'-debug.apk'))

for arch, machine in (('amd64',62), ('arm64',183)):
    binary = SOURCE/('dist/ITOM-0.9.2-Rev7-linux-'+arch)
    raw = binary.read_bytes()
    assert raw[:6] == b'\x7fELF\x02\x01' and int.from_bytes(raw[18:20],'little') == machine
    dest = STAGE/(PREFIX+'-linux-'+arch+'.bin')
    shutil.copy2(binary, dest)
    dest.chmod(0o755)
assert 'transport-rework3-rev7' in subprocess.check_output(
    [str(STAGE/(PREFIX+'-linux-amd64.bin')), 'version'], text=True)

qualified_commit = subprocess.check_output(['git','rev-parse','HEAD'],cwd=REPO,text=True).strip()
status = dict(schema=1, classification='REV7_TARGETED_VALIDATION_PRERELEASE',
              stable_release_ready=False, prerelease_assets_authorized=True,
              all_internal_gates_passed=False, all_required_targeted_checks_passed=True, final_full_suite_completed=False, internally_stability_qualified=False, throughput_floor_waived_by_user=True, mobile_crash_resolved=False,
              mobile_crash_cause='UNCONFIRMED_NO_FATAL_STACK',
              real_provider_tested=False, physical_android_device_tested=False,
              created_utc=datetime.datetime.now(datetime.timezone.utc).isoformat(),
              release_tag=TAG, qualified_source_commit=qualified_commit,
              qualification_source_sha256=identity,
              core_runtime_sha256=core['core_runtime_sha256'],
              application_identity_matches_rev7_source=True,
              apk_identity=android['apk_identity'],
              apk_sha256=android['sha256'], apk_native_libraries=android['apk_native_libraries'],
              certificate_sha256=certificate, signature_v2_verified=True,
              same_signer_as_published_rev3_testing_and_rev4=True, private_key_included=False,
              binary_revision_string='transport-rework3-rev7')
(STAGE/'STATUS.json').write_text(json.dumps(status,indent=2)+'\n')
shutil.copy2(SOURCE/'docs/rev7/evidence/rev7_validation.json', STAGE/'VALIDATION.json')
shutil.copy2(SOURCE/'docs/rev7/REV7_ANALYSIS_2026-10-16.md', STAGE/'REV7_ANALYSIS.md')
shutil.copy2(SOURCE/'HANDOFF.md', STAGE/'HANDOFF.md')
notes = (REPO/'builds/0.9.2-Rev7/RELEASE_NOTES.md').read_text()
shutil.copy2(WORK/'field-correlation.json', STAGE/'FIELD_CORRELATION.json')
(STAGE/'SIGNER.txt').write_text(signer.stdout)
(STAGE/'RELEASE_NOTES.md').write_text(notes)
assert not (SOURCE/'android/local.properties').exists()
source_zip = STAGE/(PREFIX+'-sources.zip')
package(SOURCE, source_zip)
with zipfile.ZipFile(source_zip) as z:
    assert z.testzip() is None
    names=z.namelist()
    assert not any(x.endswith(('.keystore','.jks','.key','.env')) or '/attachments/' in x for x in names)
    for name, expected in json.loads(z.read('SOURCE_MANIFEST.json')).items():
        assert hashlib.sha256(z.read(name)).hexdigest() == expected
assert source_fingerprint() == identity

# Include exact synthetic test evidence, without build logs that may contain
# environment or signing configuration. Raw user captures are never inputs.
evidence_files={
 'final-targeted.json':WORK/'final-targeted-complete.json',
 'final-targeted-race.jsonl':WORK/'final-targeted-race.jsonl',
 'recovery/minimum-service-targeted.log':WORK/'repair-minimum-service-fixed.log',
 'baseline/core-events.jsonl':WORK/'expanded-validation/core-test-events.jsonl',
 'baseline/core-report.json':WORK/'expanded-validation/core-report.json',
 'baseline/pipeline.json':WORK/'expanded-validation/pipeline.json',
}
paths=[p for p in sorted((WORK/'final-targeted-load').glob('*/results.json')) if read(p).get('source_sha256')==identity]
assert paths
report_path=paths[-1];report_data=read(report_path)
evidence_files['final-load/results.json']=report_path
for run in report_data['cases']:
 log=report_path.parent/run['log'];assert digest(log)==run['log_sha256']
 evidence_files['final-load/'+log.name]=log
evidence_manifest={name:entry(path) for name,path in evidence_files.items()}
with zipfile.ZipFile(STAGE/'TEST_EVIDENCE.zip','x',zipfile.ZIP_DEFLATED,compresslevel=6) as archive:
    for name,path in sorted(evidence_files.items()):
        archive.write(path,name)
    archive.writestr('MANIFEST.json',json.dumps(evidence_manifest,indent=2)+'\n')

(STAGE/'SHA256SUMS').write_text(''.join(digest(p)+'  '+p.name+'\n' for p in sorted(STAGE.iterdir())))
full = STAGE/(PREFIX+'-full.zip')
with zipfile.ZipFile(full,'x',zipfile.ZIP_DEFLATED,compresslevel=6) as z:
    for p in sorted(STAGE.iterdir()):
        if p != full:
            z.write(p,p.name)
(STAGE/(full.name+'.sha256')).write_text(digest(full)+'  '+full.name+'\n')
parts = ARCHIVE/'parts'
parts.mkdir()
entries=[]
with full.open('rb') as stream:
    for i in range(1,100):
        raw=stream.read(8*1024*1024)
        if not raw:break
        p=parts/('full.zip.part%03d'%i)
        p.write_bytes(raw)
        entries.append(dict(path=str(p.relative_to(REPO)),size=len(raw),sha256=digest(p)))
manifest=dict(schema=1,classification=status['classification'],stable_release_ready=False,
              all_internal_gates_passed=False,all_required_targeted_checks_passed=True,release_tag=TAG,
              qualification_source_sha256=identity,qualified_source_commit=qualified_commit,
              archive=entry(full),parts=entries,
              assets=[entry(p) for p in sorted(STAGE.iterdir()) if p.is_file()])
(ARCHIVE/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
build=REPO/'builds/0.9.2-Rev7'
build.mkdir(parents=True,exist_ok=True)
(build/'RELEASE_NOTES.md').write_text(notes)
print(json.dumps(dict(stage=str(STAGE),manifest=str(ARCHIVE/'manifest.json'),
                      archive_size=full.stat().st_size,parts=len(entries),
                      assets=manifest['assets']),indent=2))
