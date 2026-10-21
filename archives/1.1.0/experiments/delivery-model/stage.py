#!/usr/bin/env python3
"""Stage an identifiable DEV3 testing build; never overwrite frozen DEV2."""
from pathlib import Path
import argparse,hashlib,json,os,shutil,subprocess

HERE=Path(__file__).resolve().parent
ROOT=HERE.parents[3]

def main():
    p=argparse.ArgumentParser();p.add_argument('output',type=Path);a=p.parse_args();out=a.output.resolve()
    pair=out.parent/(out.name+'-paired')
    env=dict(os.environ,ITOM_MODEL_REVISION='2')
    subprocess.run(['python3',str(HERE/'prepare.py'),str(pair)],env=env,check=True)
    shutil.copytree(ROOT/'archives/1.1.0/checkpoints/DEV2/source',out,ignore=shutil.ignore_patterns('dist','build','.gradle','__pycache__','*.test'))
    shutil.rmtree(out/'core');shutil.copytree(pair/'model',out/'core')
    paths=['README.md','source-info.json','android/app/build.gradle.kts',
           'core/cmd/itom/main.go','core/manager/trace.go','core/tunnel/version.go',
           'tools/build.py','tools/build_core.py','tools/archive_main.py','tools/test_launcher.py']
    changed=[]
    for relative in paths:
        path=out/relative;text=path.read_text()
        updated=text.replace('DEV2','DEV3').replace('dev2','dev3').replace('10102','10103')
        assert text!=updated,relative
        path.write_text(updated);changed.append(relative)
    info=json.loads((out/'source-info.json').read_text())
    assert info['android_application_id']=='org.itom.client' and info['android_version_code']==10103
    assert info['revision']=='itom-1-1-0-dev3'
    provenance=json.loads((pair/'provenance.json').read_text())
    provenance['metadata_changes']=changed
    provenance['qualification']='EXPERIMENTAL DEV3. Retained100Mbps and field qualification are not met.'
    provenance['staged_source_sha256']={str(f.relative_to(out)):hashlib.sha256(f.read_bytes()).hexdigest() for f in sorted(out.rglob('*')) if f.is_file()}
    (out/'MODEL_BUILD_PROVENANCE.json').write_text(json.dumps(provenance,indent=2)+'\n')
    print('Staged ITOM1.1.0 DEV3 with the grant-aware runtime model; final1.0.0 and DEV2 unchanged')
if __name__=='__main__':main()
