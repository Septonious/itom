"""Reassemble reviewed local build artifacts; verify before release publication."""
import argparse
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import zipfile

ROOT = Path(__file__).resolve().parents[1]
MANIFEST = ROOT / 'archives/rev4/manifest.json'


def verify_file(path, entry):
    if not path.is_file() or path.is_symlink() or path.stat().st_size != entry['size']:
        raise RuntimeError('Missing or wrong-sized artifact: ' + str(path))
    with path.open('rb') as stream:
        actual = hashlib.file_digest(stream, 'sha256').hexdigest()
    if actual != entry['sha256']:
        raise RuntimeError('SHA-256 mismatch: ' + str(path))


def assemble(manifest, output):
    output.mkdir(parents=True, exist_ok=False)
    archive = output / manifest['archive']['name']
    with archive.open('xb') as target:
        for part in manifest['parts']:
            path = ROOT / part['path']
            if not path.resolve().is_relative_to(ROOT.resolve()):
                raise RuntimeError('Invalid part path')
            verify_file(path, part)
            with path.open('rb') as stream:
                shutil.copyfileobj(stream, target)
    verify_file(archive, manifest['archive'])
    with zipfile.ZipFile(archive) as bundle:
        if bundle.testzip() is not None:
            raise RuntimeError('Archive CRC failure')
        if sum(x.file_size for x in bundle.infolist()) > 256 * 1024 * 1024:
            raise RuntimeError('Unexpected archive size')
        for entry in manifest['assets']:
            name = entry['name']
            if Path(name).name != name:
                raise RuntimeError('Invalid asset name')
            target = output / name
            if target == archive:
                continue
            if name == archive.name + '.sha256':
                target.write_text(manifest['archive']['sha256'] + '  ' + archive.name + '\n')
            else:
                with bundle.open(name) as source, target.open('xb') as dest:
                    shutil.copyfileobj(source, dest)
            verify_file(target, entry)
            if '-linux-' in name:
                target.chmod(0o755)
    for line in (output / 'SHA256SUMS').read_text().splitlines():
        digest, name = line.split('  ', 1)
        if Path(name).name != name or hashlib.sha256((output / name).read_bytes()).hexdigest() != digest:
            raise RuntimeError('Bundle checksum mismatch')
    for entry in manifest['assets']:
        verify_file(output / entry['name'], entry)
    verification = json.loads((output / 'verification.json').read_text())
    if (verification.get('release_ready') is not True
            or verification.get('revision') != 'transport-rework3-rev4'
            or verification.get('all_required_tests_passed') is not True
            or verification.get('qualification', {}).get('all_lab_cases_passed') is not True):
        raise RuntimeError('Rev4 is unstable: required test or performance gate is incomplete')
    cases = verification['qualification'].get('cases', [])
    expected_cases = {mode + '-' + str(i) for mode in ('single', 'multi') for i in (1, 2, 3)}
    if (len(cases) != 6 or {case.get('name') for case in cases} != expected_cases
            or not all(case.get('ok') is True for case in cases)):
        raise RuntimeError('Every SINGLE/MULTI qualification run must pass')
    core = verification.get('core', {})
    tests = core.get('tests', {})
    pipeline = verification.get('pipeline', {})
    field = verification.get('field_suites', [])
    if (core.get('status') != 'passed' or tests.get('failed') != 0 or tests.get('skipped') != 0
            or verification.get('android', {}).get('status') != 'passed'
            or pipeline.get('complete') is not True or not pipeline.get('steps')
            or any(step.get('exit_code') != 0 for step in pipeline['steps'])
            or len(field) != 2 or [len(x.get('runs', [])) for x in field] != [3, 1]
            or not all(x.get('all_cases_passed') is True for x in field)):
        raise RuntimeError('All core, application, race and Android gates must pass before release')
    print('Verified full archive and', len(manifest['assets']), 'release assets')


def verify_release(manifest, response, repository, download_dir):
    if response.get('tag_name') != manifest['release_tag']:
        raise RuntimeError('Wrong release tag')
    actual = {entry['name']: entry for entry in response.get('assets', [])}
    if set(actual) != {entry['name'] for entry in manifest['assets']}:
        raise RuntimeError('Missing, duplicate or extra release assets')
    if len(actual) != len(response['assets']):
        raise RuntimeError('Duplicate release assets')
    for expected in manifest['assets']:
        remote = actual[expected['name']]
        if remote.get('size') != expected['size'] or remote.get('state') != 'uploaded':
            raise RuntimeError('Incomplete release asset: ' + expected['name'])
        digest = remote.get('digest')
        if digest:
            if digest != 'sha256:' + expected['sha256']:
                raise RuntimeError('Remote digest mismatch: ' + expected['name'])
        else:
            # Older API responses may omit digest. Verify downloaded bytes;
            # absence of a server checksum must never silently qualify.
            download_dir.mkdir(parents=True, exist_ok=True)
            subprocess.run(['gh', 'release', 'download', manifest['release_tag'],
                            '--repo', repository, '--pattern', expected['name'],
                            '--dir', str(download_dir)], check=True)
            verify_file(download_dir / expected['name'], expected)
    print('Verified remote release sizes and SHA-256 for every asset')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path)
    parser.add_argument('--verify-release-json', type=Path)
    parser.add_argument('--repository')
    args = parser.parse_args()
    manifest = json.loads(MANIFEST.read_text())
    if args.verify_release_json:
        if not args.repository or not args.output:
            parser.error('remote verification requires repository and output')
        verify_release(manifest, json.loads(args.verify_release_json.read_text()),
                       args.repository, args.output)
    elif args.output:
        assemble(manifest, args.output)
    else:
        parser.error('output required')


if __name__ == '__main__':
    main()
