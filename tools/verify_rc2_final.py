#!/usr/bin/env python3
"""Fail if final changes RC2 production, Android runtime, or qualification policy."""
import argparse,hashlib,json,subprocess
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--output',type=Path);a=p.parse_args()
r=Path(__file__).resolve().parents[1];s=r/'archives/1.0.0/source'
def git(*v):return subprocess.check_output(['git',*v],cwd=r)
def baseline(name):return git('show','v1.0.0-rc2:archives/1.0.0-rc2/source/'+name)
rows=[]; renamed={'core/tunnel/version.go','core/cmd/itom/main.go','core/manager/trace.go'}
paths=git('ls-tree','-r','--name-only','v1.0.0-rc2:archives/1.0.0-rc2/source').decode().splitlines()
for name in paths:
 prod=(name.startswith('core/') and not name.endswith('_test.go'))
 android=name.startswith(('android/app/src/','android/domain/','native/'))
 qualify=name in ('tools/transport_qualification.py',)
 if not(prod or android or qualify):continue
 old=baseline(name); new=(s/name).read_bytes()
 expected=old.replace(b'itom-1-0-0-rc2',b'itom-1-0-0-final') if name in renamed else old
 assert new==expected, 'Non-RC2 production change: '+name
 rows.append({'path':name,'rc2_sha256':hashlib.sha256(old).hexdigest(),'final_sha256':hashlib.sha256(new).hexdigest(),'identity_string_only':name in renamed})
# No unreviewed production addition is permitted either.
for f in (s/'core').rglob('*.go'):
 if not f.name.endswith('_test.go'):assert f.relative_to(s).as_posix() in paths, 'Added runtime: '+str(f)
info=json.loads((s/'source-info.json').read_text());g=(s/'android/app/build.gradle.kts').read_text()
assert info['android_application_id']=='org.itom.client' and 'applicationIdSuffix' not in g
assert 'versionCode = 10014; versionName = "1.0.0"' in g
result={'baseline':'a50a6a9e31ee9b38b53e84c9dce645e7ed7e415b','checked_files':len(rows),'production_core_files':sum(x['path'].startswith('core/') for x in rows),'transport_policy':'RC2 unchanged','android_runtime':'RC2 unchanged; current package identity retained','qualification_thresholds':'RC2 unchanged','files':rows}
if a.output:a.output.parent.mkdir(parents=True,exist_ok=True);a.output.write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps({k:v for k,v in result.items() if k!='files'}))
