#!/usr/bin/env python3
"""Pair immutable RC2/RC10 production cores with the identical healthy fixtures.

Only *_test.go files are overlaid. Never patch production code to make RC2
look current. Missing optional diagnostic fields are reported as unavailable.
"""
import argparse
import hashlib
import io
import json
import re
from pathlib import Path
import subprocess
import tarfile

FIXTURES = (
    'healthy_endpoints_test.go', 'imap_test.go', 'mobile_network_test.go',
    'provider_network_test.go', 'supervised_load_test.go',
    'supervisor_fixture_test.go', 'field_replay_test.go',
    'rc6_stability_gate_test.go', 'rc7_speedup_profile_test.go',
    'rc8_release_gate_test.go',
)
ROOT = Path(__file__).resolve().parents[3]


def git(*args):
    return subprocess.check_output(['git', '-C', str(ROOT), *args])


def digest(data):
    return hashlib.sha256(data).hexdigest()


def files_at(ref, prefix):
    result = {}
    with tarfile.open(fileobj=io.BytesIO(git('archive', ref, prefix))) as archive:
        for member in archive.getmembers():
            if member.isfile() and member.name.startswith(prefix):
                relative = member.name[len(prefix):]
                assert relative and '..' not in Path(relative).parts
                result[relative] = archive.extractfile(member).read()
    assert 'go.mod' in result and 'tunnel/version.go' in result
    return result


def production(files):
    return {p: digest(b) for p, b in sorted(files.items()) if not p.endswith('_test.go')}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('output', type=Path)
    args = parser.parse_args()
    out = args.output.resolve()
    out.mkdir(parents=True, exist_ok=False)
    candidate = git('rev-parse', 'HEAD').decode().strip()
    baseline = git('rev-parse', 'v1.0.0-rc2^{commit}').decode().strip()
    sources = {
        'rc2': files_at(baseline, 'archives/1.0.0-rc2/source/core/'),
        'rc10': files_at(candidate, 'archives/1.0.0-rc10/source/core/'),
    }
    common = {f'tunnel/{name}': sources['rc10'][f'tunnel/{name}'] for name in FIXTURES}
    # RC10 added optional diagnostic fields. Access them reflectively in BOTH
    # copies of the common harness, returning -1 when RC2 has no such metric.
    # No byte clocks, gates, workload, probes or assertions are changed.
    key = 'tunnel/supervised_load_test.go'
    text = common[key].decode()
    for owner, fields in {
        'c': ('LastBulkACKAgeMS', 'LastBulkACKSinceMS'),
        'ss': ('LastBulkACKAgeMS', 'LastBulkACKSinceMS'),
        'd': ('DataFetchPayloadBytes', 'DataFetchCallbackMaxMS'),
        'diagRead': ('DataFetchCallbackTotalMS', 'DataFetchCallbackMaxMS', 'DataFetchPayloadBytes'),
    }.items():
        for field in fields:
            old = f'{owner}.{field}'
            pattern = r'\b' + re.escape(old) + r'\b'
            assert len(re.findall(pattern, text)) == 1, old
            text = re.sub(pattern, f'comparisonDiagnosticInt({owner}, "{field}")', text)
    common[key] = text.encode()
    common['tunnel/comparison_compat_test.go'] = b'''package tunnel
import "reflect"
// -1 means the historical production diagnostic did not expose this field.
// This helper reads optional logs only, never workload or acceptance state.
func comparisonDiagnosticInt(snapshot any, field string) int64 {
 v := reflect.ValueOf(snapshot).FieldByName(field)
 if !v.IsValid() { return -1 }
 if v.CanInt() { return v.Int() }
 if v.CanUint() { return int64(v.Uint()) }
 panic("unsupported comparison diagnostic")
}
'''
    manifest = dict(baseline_commit=baseline, candidate_commit=candidate,
                    production_unmodified=True, fixtures={p: digest(b) for p, b in sorted(common.items())},
                    diagnostic_compatibility='Optional logs only; missing RC2 metrics are -1', variants={})
    for variant, original in sources.items():
        files = original | common
        assert production(files) == production(original)
        core = out / variant
        for relative, data in files.items():
            path = core / relative
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(data)
        actual = {str(p.relative_to(core)): p.read_bytes() for p in core.rglob('*') if p.is_file()}
        assert production(actual) == production(original)
        manifest['variants'][variant] = dict(production=production(actual),
            production_sha256=digest(json.dumps(production(actual), sort_keys=True).encode()))
    (out / 'comparison-manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
    print(json.dumps({k: manifest[k] for k in ('baseline_commit', 'candidate_commit', 'production_unmodified')}))


if __name__ == '__main__':
    main()
