#!/usr/bin/env python3
"""Test earlier outbound proof scheduling against accelerated inbound cleanup."""
import argparse, hashlib, json, os, shutil, subprocess
from pathlib import Path
HERE=Path(__file__).resolve().parent

def main():
 p=argparse.ArgumentParser();p.add_argument('output',type=Path);a=p.parse_args();out=a.output.resolve()
 subprocess.run(['python3',str(HERE.parent/'retained-response/prepare.py'),str(out)],check=True)
 divisor=int(os.environ.get('ITOM_LOCAL_CLEANUP_DIVISOR','8'));assert divisor in (4,8,16)
 t=out/'model/tunnel';path=t/'cleanup.go';s=path.read_text()
 old='(window-localFastReserveBytes)/4';assert s.count(old)==1
 path.write_text(s.replace(old,f'(window-localFastReserveBytes)/{divisor}'))
 shutil.copyfile(HERE/'headroom_test.go',t/'duplex_reclamation_test.go')
 # The test receives the exact experimental policy, without a production env knob.
 test=t/'duplex_reclamation_test.go';test.write_text(test.read_text().replace('EXPERIMENT_DIVISOR',str(divisor)))
 subprocess.run(['gofmt','-w',str(path),str(test)],check=True)
 prov=json.loads((out/'provenance.json').read_text())
 prov['retained_response_production']=prov['model_production']
 prov['model_production']={str(f.relative_to(out/'model')):hashlib.sha256(f.read_bytes()).hexdigest() for f in sorted((out/'model').rglob('*.go')) if not f.name.endswith('_test.go')}
 prov['experiment']+='; earlier local outbound proof eligibility derived from recovered byte window'
 prov['local_cleanup_divisor']=divisor
 prov['production_delta']=[r for r,h in prov['model_production'].items() if prov['previous_production'].get(r)!=h]
 (out/'provenance.json').write_text(json.dumps(prov,indent=2)+'\n')
 print('Candidate production delta:',prov['production_delta'])
if __name__=='__main__':main()
