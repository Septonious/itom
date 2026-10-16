"""Assemble and verify the exact qualified Rev7 prerelease assets."""
import argparse
import hashlib
import json
import shutil
import subprocess
from pathlib import Path, PurePosixPath
import zipfile

ROOT = Path(__file__).resolve().parents[1]
MANIFEST = ROOT / 'archives/rev7/manifest.json'
CLASSIFICATION = 'REV7_LAB_QUALIFIED_PRERELEASE'


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
    require(status['classification'] == CLASSIFICATION and status['stable_release_ready'] is False,
            'Field validation must remain explicit')
    require(status['release_tag'] == manifest['release_tag'], 'Wrong testing tag')
    require(status['all_internal_gates_passed'] is True and evidence['all_internal_gates_passed'] is True,
            'Every internal gate must pass')
    require(status['mobile_crash_resolved'] is False and evidence['stable_release_ready'] is False,
            'Unconfirmed field crash must remain disclosed')
    require(status['qualification_source_sha256'] == evidence['qualification_source_sha256'] ==
            manifest['qualification_source_sha256'], 'Qualification source mismatch')
    require(status['qualified_source_commit'] == manifest['qualified_source_commit'], 'Source commit mismatch')
    require(status['core_runtime_sha256'] == evidence['first_party_core_runtime_sha256'], 'Runtime mismatch')
    core = evidence['core']
    require(core['passed_test_events'] > 0 and core['failed'] == core['skipped'] == 0, 'Core gate failed')
    require(evidence['imap_fork_tests'] == evidence['vet'] == 'passed', 'IMAP/vet gate failed')
    for group in ('android_domain_tests', 'android_callback_adapter_tests'):
        tests = evidence[group]
        require(tests['tests'] > 0 and tests['failures'] == tests['errors'] == tests['skipped'] == 0,
                'Android gate failed: ' + group)
    require(evidence['android_lint']['errors'] == 0, 'Lint errors')
    fields = evidence['field_repetitions']
    require(len(fields) == 2 and [x['repetitions'] for x in fields] == [3, 1] and
            all(x['all_passed'] and x['cases_per_repetition'] == 49 for x in fields), 'Field suite failed')
    require(bool(evidence['race_checks']) and all(x['passed_test_events'] > 0 and
            x['failed'] == x['skipped'] == 0 for x in evidence['race_checks'].values()), 'Race gate failed')
    steps = evidence['pipeline_steps']
    required_steps={'candidate-python','candidate-android','candidate-field-repeated','candidate-field-commit-delay','candidate-core-server','candidate-race-accounting','candidate-race-recovery','candidate-native-vet','candidate-admin-native-race','candidate-speed','candidate-speed-retained'}
    require(len(steps) == 11 and {x['name'] for x in steps} == required_steps and all(x['passed'] and x['exit_code'] == 0 for x in steps), 'Incomplete pipeline')
    admin=evidence['admin_features']
    require(admin['server_authoritative_role'] and admin['additional_private_key_challenge'] and admin['durable_independent_watchdog'] and admin['loss_timeout_seconds']==60 and admin['healthy_trial_seconds']==60 and admin['irreversible_operations_cli_only'] and admin['native_imap_reconnect_race_test'] and admin['actual_server_supervisor_restart_tested'] and admin['server_restart_recovery_ms']<60000, 'Admin safety gates incomplete')
    criteria = evidence['speed_criteria']
    require(criteria['tolerance_percent'] == 10 and criteria['useful_upload_mbps_min'] == 90 and
            criteria['useful_download_mbps_min'] == 90 and
            criteria['all_loaded_rtt_ns_strictly_below'] == 1100000000, 'Changed acceptance limits')
    require(evidence['retained_speed_scenario']['retained_state'] is True, 'Missing retained-state matrix')
    for group in ('speed_cases','retained_speed_cases'):
        cases = evidence[group]
        require({x['name'] for x in cases} == {f'{mode}-{i}' for mode in ('single','multi') for i in range(1,4)}
                and len(cases) == 6, 'Missing fresh qualification cases')
        for case in cases:
            accepted = case['supervised_acceptance']
            probes = case['supervised_result']
            require(case['passed'] and accepted['accepted'] == 'true' and
                    float(accepted['minimum_verified_mbps']) >= 90 and int(accepted['max_rtt_ns']) < 1100000000
                    and probes['timeouts'] == probes['missed'] == '0' and probes['rtt_samples'] == probes['overlap'] == '30',
                    'Speed/RTT gate failed: ' + case['name'])
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
                'Source archive differs from the fully qualified candidate')


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
    print('Verified',len(asset_names),'assets; all internal gates passed; Rev7 prerelease; physical field validation pending')


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
    print('Verified every remote Rev7 asset size and SHA-256')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output',type=Path,required=True)
    parser.add_argument('--verify-release-json',type=Path)
    parser.add_argument('--repository')
    parser.add_argument('--expected-commit')
    args = parser.parse_args()
    manifest = json.loads(MANIFEST.read_text())
    require(manifest['classification'] == CLASSIFICATION and manifest['stable_release_ready'] is False and
            manifest['all_internal_gates_passed'] is True, 'Manifest must describe qualified Rev7 prerelease assets')
    if args.verify_release_json:
        require(bool(args.repository and args.expected_commit), 'Remote verification needs repo and commit')
        verify_remote(manifest,json.loads(args.verify_release_json.read_text()),args.repository,
                      args.output,args.expected_commit)
    else:
        assemble(manifest,args.output)


if __name__ == '__main__':
    main()
