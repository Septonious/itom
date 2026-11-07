"""Bounded writer recovery audit against immutable 1.1.4 testing source."""
import hashlib,json,subprocess
from pathlib import Path
repo=Path(__file__).resolve().parents[3]
root=repo/'archives/1.1.5/source'
base='bfefec8a75d6eca0f57cccf0696c31fa2dfea1f5'
prefix='archives/1.1.4/source/'
def runtime(p):return p.name in ('go.mod','go.sum')or p.suffix=='.go'and not p.name.endswith('_test.go')
prior={n.removeprefix(prefix)for n in subprocess.check_output(['git','ls-tree','-r','--name-only',base,'--',prefix],cwd=repo,text=True).splitlines()if n.removeprefix(prefix).split('/')[0]in('core','mailstream','native')and runtime(Path(n))}
current={p.relative_to(root).as_posix()for d in('core','mailstream','native')for p in(root/d).rglob('*')if p.is_file()and runtime(p)}
assert current==prior
approved=json.loads((Path(__file__).parent/'approved_runtime.json').read_text())
changed=[]
for name in sorted(current):
 after=(root/name).read_bytes()
 before=subprocess.check_output(['git','show',base+':'+prefix+name],cwd=repo)if name in prior else None
 if name in approved:
  assert before!=after and hashlib.sha256(after).hexdigest()==approved[name],name
  changed.append(name)
 else:assert before==after,name
assert all(n in changed for n in ('mailstream/imap.go','mailstream/append_writers.go','core/tunnel/api.go'))
# No controller, wire format, quotas, ownership audit or native implementation changes.
for name in ('mailstream/delivery.go','mailstream/pacing.go','mailstream/control_storage.go','mailstream/retention.go','mailstream/reclamation.go','mailstream/maintenance.go','mailstream/storage.go'):
 assert name not in changed,name
info=json.loads((root/'source-info.json').read_text())
assert(info['android_application_id'],info['android_version_name'],info['android_version_code'])==('org.itom.client','1.1.5',10150)
out=repo/'final-audit';out.mkdir(exist_ok=True)
report=dict(verified=True,baseline_commit=base,runtime_files_compared=len(current),only_runtime_changes=changed,approved_sha256=approved,controller_identical=True,protocol_and_mailbox_limits_identical=True,server_only_cleanup_preserved=True)
(out/'TRANSPORT_RESTORE.json').write_text(json.dumps(report,indent=2)+'\n')
print(json.dumps(report,indent=2))
