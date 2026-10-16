"""Assemble and verify the exact qualified Rev10 prerelease assets."""
import argparse
import hashlib
import json
import shutil
import subprocess
from pathlib import Path, PurePosixPath
import zipfile

ROOT = Path(__file__).resolve().parents[1]
MANIFEST = ROOT / 'archives/rev10/manifest.json'
CLASSIFICATION = 'REV10_TARGETED_TESTING_BUILD'


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def basename(name):
    require(name and Path(name).name == name and name not in ('.', '..'), 'Unsafe asset name')
    return name


def verify_file(path, expected):
    require(path.stat().st_size == expected['size'], 'Wrong size: ' + path.name)
    require(digest(path) == expected['sha256'], 'Wrong digest: ' + path.name)


def verify_gates(manifest, output):
    status = json.loads((output/'STATUS.json').read_text())
    evidence = json.loads((output/'VALIDATION.json').read_text())
    require(status['classification'] == CLASSIFICATION and evidence['classification'] == CLASSIFICATION,
            'Testing classification missing')
    require(status['stable_release_ready'] is False and evidence['stable_release_ready'] is False and
            status['all_internal_gates_passed'] is False and evidence['all_internal_gates_passed'] is False,
            'Incomplete qualification must remain explicit')
    require(status['release_tag'] == manifest['release_tag'], 'Wrong testing tag')
    require(status['qualification_source_sha256'] == evidence['qualification_source_sha256'] ==
            manifest['qualification_source_sha256'], 'Artifact source mismatch')
    require(status['qualified_source_commit'] == manifest['qualified_source_commit'], 'Source commit mismatch')
    require(status['core_runtime_sha256'] == evidence['first_party_core_runtime_sha256'], 'Runtime mismatch')
    require(evidence['final_full_suite_completed'] is False and evidence['user_requested_targeted_finalization'] is True,
            'Targeted scope must remain explicit')
    require(evidence['targeted_correctness_passed'] and evidence['android_build_passed'], 'Required build checks failed')
    for group in ('android_domain_tests','android_app_tests'):
        tests=evidence[group]
        require(tests['tests']>0 and tests['failures']==tests['errors']==tests['skipped']==0,'Android checks failed')
    require(evidence['android_lint']['errors']==0,'Lint failed')
    require(evidence['performance_retested'] is False and evidence['strict_performance_passed'] is False,
            'Unmeasured performance must not be certified')
    require(evidence['strict_speed_target_mbps'] == 100 and evidence['speed_tolerance_percent'] == 0,
            'The existing strict target must remain unchanged')
    require(status['certificate_sha256'] == '39667cd3f91521b09af012b2ca08f8f0ba0d7e07f14b32a9a1e794cb3f4bf999',
            'Unexpected APK signer')
    require((output/'HANDOFF.md').stat().st_size>4000,'Full handoff missing')
    with zipfile.ZipFile(output/'TEST_EVIDENCE.zip') as logs:
        require(logs.testzip() is None,'Evidence ZIP corruption')
        index=json.loads(logs.read('MANIFEST.json'))
        require(len(logs.namelist())==len(set(logs.namelist())) and set(logs.namelist())==set(index)|{'MANIFEST.json'},'Evidence entries mismatch')
        for name, expected in index.items():
            raw=logs.read(name)
            require(len(raw)==expected['size'] and hashlib.sha256(raw).hexdigest()==expected['sha256'],'Evidence digest mismatch')
        raw=logs.read('final-correctness-race.jsonl')
        events=[json.loads(x) for x in raw.decode().splitlines() if x.startswith('{')]
        require(not any(x.get('Action') in ('fail','skip') for x in events),'Targeted correctness failed')
        require(any(x.get('Action')=='pass' and not x.get('Test') for x in events),'Targeted correctness incomplete')
        required = {'TestRev10DiscoveryThenAllClientSocketsLost', 'TestRev10ControlReplacementRetryBoundaries',
                    'TestRev7ControlAndReceiveRepairBypassQueuedOptionalLogins'}
        require(required <= {x.get('Test') for x in events if x.get('Action') == 'pass'}, 'Required race tests missing')
        require(any('active_lines_restored=20 original_tcp_retained=true' in x.get('Output','') for x in events),
                'All-line recovery and original TCP evidence missing')
        normal = [json.loads(x) for x in logs.read('normal-recovery-integration.jsonl').decode().splitlines() if x.startswith('{')]
        require(not any(x.get('Action') in ('fail','skip') for x in normal) and
                any(x.get('Action') == 'pass' and not x.get('Test') for x in normal), 'Normal recovery failed/incomplete')
        require(any(x.get('Test') == 'TestRev7CorrelatedControlEOFBetweenTransfersKeepsEveryCarrier' and x.get('Action') == 'pass' for x in normal),
                'Post-load control EOF regression missing')
        baseline = [json.loads(x) for x in logs.read('before-fix-regression.jsonl').decode().splitlines() if x.startswith('{')]
        require(any('transient repair destroyed original carrier' in x.get('Output','') for x in baseline), 'Before-fix regression evidence missing')
    apks = list(output.glob('*.apk'))
    require(len(apks) == 1 and digest(apks[0]) == status['apk_sha256'], 'APK identity mismatch')
    with zipfile.ZipFile(apks[0]) as apk:
        require(apk.testzip() is None, 'APK ZIP corruption')
        for name, expected in status['apk_native_libraries'].items():
            require(hashlib.sha256(apk.read(name)).hexdigest() == expected, 'Native library mismatch')
    for arch, machine in (('amd64',62), ('arm64',183)):
        matches = list(output.glob('*-linux-'+arch+'.bin'))
        require(len(matches) == 1, 'Missing Linux architecture')
        raw = matches[0].read_bytes()
        require(raw[:6] == b'\x7fELF\x02\x01' and int.from_bytes(raw[18:20],'little') == machine,
                'Wrong Linux executable architecture')
        matches[0].chmod(0o755)
    sources = list(output.glob('*-sources.zip'))
    require(len(sources) == 1, 'Missing sources')
    with zipfile.ZipFile(sources[0]) as source:
        require(source.testzip() is None, 'Source ZIP corruption')
        for name, expected in json.loads(source.read('SOURCE_MANIFEST.json')).items():
            require(hashlib.sha256(source.read(name)).hexdigest() == expected, 'Source manifest mismatch')
        # Match the qualification tool's path ordering and complete source
        # fingerprint without executing any code from the source archive.
        identity = hashlib.sha256()
        names = source.namelist()
        for folder in ('core','native','android','tools'):
            candidates = [PurePosixPath(name).relative_to('itom') for name in names
                          if name.startswith('itom/'+folder+'/')]
            for path in sorted(candidates):
                if path.suffix in ('.go','.mod','.sum','.py','.kt','.kts','.xml','.json') and not any(
                        part in path.parts for part in ('build','.gradle','__pycache__','dist','reports')):
                    identity.update(str(path).encode()+b'\0'+source.read('itom/'+str(path))+b'\0')
        identity.update(source.read('itom/source-info.json'))
        require(identity.hexdigest() == manifest['qualification_source_sha256'],
                'Source archive differs from the packaged candidate')


def assemble(manifest, output):
    output.mkdir(parents=True, exist_ok=False)
    full = output / basename(manifest['archive']['name'])
    with full.open('xb') as stream:
        for part in manifest['parts']:
            path = (ROOT/part['path']).resolve()
            require(path.is_relative_to(ROOT.resolve()), 'Unsafe archive part path')
            verify_file(path, part)
            with path.open('rb') as source:
                shutil.copyfileobj(source, stream)
    verify_file(full, manifest['archive'])
    full_sha = full.name+'.sha256'
    asset_names = [basename(x['name']) for x in manifest['assets']]
    require(len(asset_names) == len(set(asset_names)), 'Duplicate assets')
    with zipfile.ZipFile(full) as bundle:
        require(bundle.testzip() is None, 'Archive corruption')
        require(len(bundle.namelist()) == len(set(bundle.namelist())) and
                set(bundle.namelist()) == set(asset_names)-{full.name,full_sha}, 'Unexpected archive entries')
        for name in bundle.namelist():
            with bundle.open(name) as source, (output/basename(name)).open('xb') as target:
                shutil.copyfileobj(source,target)
    (output/full_sha).write_text(manifest['archive']['sha256']+'  '+full.name+'\n')
    for expected in manifest['assets']:
        verify_file(output/expected['name'], expected)
    checksum_names = set()
    for line in (output/'SHA256SUMS').read_text().splitlines():
        expected,name = line.split('  ',1)
        require(digest(output/basename(name)) == expected, 'Checksum list mismatch')
        require(name not in checksum_names, 'Duplicate checksum entry')
        checksum_names.add(name)
    require(checksum_names == set(asset_names)-{full.name,full_sha,'SHA256SUMS'}, 'Incomplete checksum list')
    verify_gates(manifest, output)
    print('Verified',len(asset_names),'assets; targeted correctness checked; targeted results and qualification limits verified; Rev10 testing build')


def verify_remote(manifest, response, repository, output, expected_commit):
    require(response['tag_name'] == manifest['release_tag'] and response['prerelease'] is True,
            'Wrong release tag or classification')
    require(response['target_commitish'] == expected_commit, 'Wrong publication commit')
    actual = {x['name']:x for x in response.get('assets',[])}
    require(len(actual) == len(response.get('assets',[])) and
            set(actual) == {x['name'] for x in manifest['assets']}, 'Remote asset set mismatch')
    for expected in manifest['assets']:
        remote = actual[expected['name']]
        require(remote['state'] == 'uploaded' and remote['size'] == expected['size'], 'Remote size/state mismatch')
        if remote.get('digest'):
            require(remote['digest'] == 'sha256:'+expected['sha256'], 'Remote digest mismatch')
        else:
            output.mkdir(parents=True,exist_ok=True)
            subprocess.run(['gh','release','download',manifest['release_tag'],'--repo',repository,
                            '--pattern',expected['name'],'--dir',str(output)],check=True)
            verify_file(output/expected['name'],expected)
    print('Verified every remote Rev10 asset size and SHA-256')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output',type=Path,required=True)
    parser.add_argument('--verify-release-json',type=Path)
    parser.add_argument('--repository')
    parser.add_argument('--expected-commit')
    args = parser.parse_args()
    manifest = json.loads(MANIFEST.read_text())
    require(manifest['classification'] == CLASSIFICATION and manifest['stable_release_ready'] is False and
            manifest['all_internal_gates_passed'] is False, 'Manifest must describe unstable testing assets')
    if args.verify_release_json:
        require(bool(args.repository and args.expected_commit), 'Remote verification needs repo and commit')
        verify_remote(manifest,json.loads(args.verify_release_json.read_text()),args.repository,
                      args.output,args.expected_commit)
    else:
        assemble(manifest,args.output)


if __name__ == '__main__':
    main()
