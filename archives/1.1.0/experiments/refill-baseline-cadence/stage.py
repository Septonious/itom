#!/usr/bin/env python3
"""Reproduce the selected model-only DEV4 candidate without changing frozen inputs."""
from pathlib import Path
import argparse, hashlib, json, os, shutil, subprocess
HERE=Path(__file__).resolve().parent
ROOT=HERE.parents[3]

def main():
 p=argparse.ArgumentParser();p.add_argument('output',type=Path);a=p.parse_args();out=a.output.resolve()
 pair=out.parent/(out.name+'-paired')
 env=dict(os.environ,ITOM_RETAINED_GC_FLOOR_MS='1100',ITOM_LOCAL_CLEANUP_DIVISOR='16',ITOM_REFILL_TRIGGER='empty')
 subprocess.run(['python3',str(HERE/'prepare.py'),str(pair)],env=env,check=True)
 shutil.copytree(ROOT/'archives/1.1.0/checkpoints/DEV2/source',out,ignore=shutil.ignore_patterns('dist','build','.gradle','__pycache__','*.test'))
 shutil.rmtree(out/'core');shutil.copytree(pair/'model',out/'core')
 paths=['README.md','source-info.json','android/app/build.gradle.kts','core/cmd/itom/main.go','core/manager/trace.go','core/tunnel/version.go','tools/build.py','tools/build_core.py','tools/archive_main.py','tools/test_launcher.py']
 for relative in paths:
  path=out/relative;s=path.read_text();updated=s.replace('DEV2','DEV4').replace('dev2','dev4').replace('10102','10104')
  assert s!=updated,relative;path.write_text(updated)
 info=json.loads((out/'source-info.json').read_text())
 assert info['android_application_id']=='org.itom.client' and info['android_version_code']==10104 and info['revision']=='itom-1-1-0-dev4'
 prov=json.loads((pair/'provenance.json').read_text())
 prov['metadata_changes']=paths
 prov['qualification']='Experimental DEV4 testing candidate; retain individual qualification failures and mixed idle tails; not field qualified'
 prov['staged_source_sha256']={str(f.relative_to(out)):hashlib.sha256(f.read_bytes()).hexdigest() for f in sorted(out.rglob('*')) if f.is_file()}
 (out/'MODEL_BUILD_PROVENANCE.json').write_text(json.dumps(prov,indent=2)+'\n')
 print('Staged DEV4 candidate only; stable1.0, RC2 and active DEV3 unchanged')
if __name__=='__main__':main()
