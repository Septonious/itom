#!/usr/bin/env python3
"""Immutable RC2 production, byte-identical shared fixtures, isolated candidate."""
import argparse,hashlib,io,json,shutil,subprocess,tarfile
from pathlib import Path
ROOT=Path(__file__).resolve().parents[3]
SOURCE=ROOT/'archives/1.1.0/source/core'
EXCLUDE={'tunnel/response_wait_test.go','datapath/socket_read_policy_test.go','tunnel/fast_refill_test.go'}
def hashes(root):
 return {str(p.relative_to(root)):hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(root.rglob('*')) if p.is_file() and not p.name.endswith('_test.go')}
def main():
 p=argparse.ArgumentParser();p.add_argument('output',type=Path);a=p.parse_args();out=a.output.resolve();out.mkdir(parents=True,exist_ok=False)
 raw=subprocess.check_output(['git','archive','v1.0.0-rc2:archives/1.0.0-rc2/source/core'],cwd=ROOT)
 baseline=out/'rc2';baseline.mkdir()
 with tarfile.open(fileobj=io.BytesIO(raw)) as t:t.extractall(baseline,filter='data')
 before=hashes(baseline);shared={}
 for f in SOURCE.rglob('*_test.go'):
  relative=str(f.relative_to(SOURCE))
  if relative in EXCLUDE:continue
  target=baseline/relative;target.parent.mkdir(parents=True,exist_ok=True);shutil.copyfile(f,target);shared[relative]=hashlib.sha256(f.read_bytes()).hexdigest()
 assert before==hashes(baseline),'reference production changed'
 shutil.copytree(SOURCE,out/'v110',ignore=shutil.ignore_patterns('__pycache__','*.test'))
 report=dict(baseline='v1.0.0-rc2',production_reference_unchanged=True,baseline_production=before,candidate_production=hashes(out/'v110'),shared_test_files=shared,excluded_candidate_only_tests=sorted(EXCLUDE))
 (out/'provenance.json').write_text(json.dumps(report,indent=2)+'\n')
 print('Reference production unchanged; shared fixture files',len(shared))
if __name__=='__main__':main()
