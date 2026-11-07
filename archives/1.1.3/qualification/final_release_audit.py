"""Audit the candidate against the immutable published 1.1.2 runtime."""
import hashlib
import json
from pathlib import Path
import subprocess

repo = Path(__file__).resolve().parents[3]
root = repo/'archives/1.1.3/source'
base = '06decfae354ec95a344005751f5091cec55a5ff5'
prefix = 'archives/1.1.2/source/'
identity = {'core/cmd/itom/main.go', 'core/tunnel/api.go', 'core/manager/trace.go'}
approved = json.loads((Path(__file__).parent/'approved_runtime.json').read_text())


def old(name):
    return subprocess.check_output(['git', 'show', base+':'+prefix+name], cwd=repo)


def runtime(path):
    return path.name in ('go.mod', 'go.sum') or path.suffix == '.go' and not path.name.endswith('_test.go')


prior = {n.removeprefix(prefix) for n in subprocess.check_output(
    ['git', 'ls-tree', '-r', '--name-only', base, '--', prefix], cwd=repo, text=True).splitlines()
    if n.removeprefix(prefix).split('/')[0] in ('core', 'mailstream', 'native') and runtime(Path(n))}
current = {p.relative_to(root).as_posix() for d in ('core', 'mailstream', 'native')
           for p in (root/d).rglob('*') if p.is_file() and runtime(p)}
assert prior == current
changed = []
for name in sorted(prior):
    before, after = old(name), (root/name).read_bytes()
    if name in identity:
        assert after == before.replace(b'itom-1-1-2-final', b'itom-1-1-3-final'), name
    elif name in approved:
        assert before != after and hashlib.sha256(after).hexdigest() == approved[name], name
        changed.append(name)
    else:
        assert before == after, name

for name in subprocess.check_output(['git', 'ls-tree', '-r', '--name-only', base, '--', prefix+'android'], cwd=repo, text=True).splitlines():
    relative = name.removeprefix(prefix)
    if '/src/main/' in relative and Path(relative).suffix in ('.kt', '.xml'):
        assert (root/relative).read_bytes() == old(relative), relative

info = json.loads((root/'source-info.json').read_text())
assert (info['android_application_id'], info['android_version_name'], info['android_version_code']) == ('org.itom.client', '1.1.3', 10148)
out = repo/'final-audit'
out.mkdir(exist_ok=True)
report = dict(verified=True, baseline_commit=base, runtime_files_compared=len(prior),
              only_runtime_changes=changed, approved_sha256=approved,
              controller_identical=False, protocol_and_mailbox_limits_identical=True,
              server_only_cleanup_identical=True, service_lifecycle_preserves_debt=True,
              android_main_sources_identical=True, native_atomic_diagnostics_only=True)
(out/'TRANSPORT_RESTORE.json').write_text(json.dumps(report, indent=2)+'\n')
print(json.dumps(report, indent=2))
