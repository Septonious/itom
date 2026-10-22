#!/usr/bin/env python3
"""Three exact production variants with byte-identical field sequence fixtures."""
from pathlib import Path
import argparse,shutil,subprocess,json,hashlib
HERE=Path(__file__).resolve().parent
ROOT=HERE.parents[3]
def hashes(root):return {str(p.relative_to(root)):hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(root.rglob('*.go')) if not p.name.endswith('_test.go')}
def main():
 p=argparse.ArgumentParser();p.add_argument('output',type=Path);a=p.parse_args();out=a.output.resolve()
 subprocess.run(['python3',str(HERE.parent/'refill-baseline-cadence/prepare.py'),str(out)],check=True)
 shutil.rmtree(out/'model');shutil.copytree(ROOT/'archives/1.1.0/checkpoints/DEV4/source/core',out/'dev4')
 shutil.copytree(ROOT/'archives/1.1.0/source/core',out/'model',ignore=shutil.ignore_patterns('*.test','__pycache__'))
 shared=('imap_test.go','field_command_service_test.go','field_sequence_itomtest_test.go')
 before=hashes(out/'rc2')
 for variant in ('rc2','dev4','model'):
  for name in shared:shutil.copyfile(ROOT/'archives/1.1.0/source/core/tunnel'/name,out/variant/'tunnel'/name)
 assert hashes(out/'rc2')==before
 (out/'FIELD_PROVENANCE.json').write_text(json.dumps({'reference_production_unchanged':True,'model_production':hashes(out/'model'),'dev4_production':hashes(out/'dev4'),'rc2_production':before,'shared':{n:hashlib.sha256((out/'model/tunnel'/n).read_bytes()).hexdigest() for n in shared}},indent=2)+'\n')
if __name__=='__main__':main()
