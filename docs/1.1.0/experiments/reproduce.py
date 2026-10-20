#!/usr/bin/env python3
"""Reproduce isolated RC2-based lab trees; run serially after all builds finish."""
import argparse,io,json,os,subprocess,tarfile
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--work',type=Path,required=True);p.add_argument('--build-only',action='store_true');a=p.parse_args()
r=Path(__file__).resolve().parents[3];e=Path(__file__).resolve().parent;w=a.work.resolve();w.mkdir(parents=True,exist_ok=True)
def run(args,**kw):return subprocess.run(args,check=True,**kw)
variants=['control','tiny','hot','combined','event','micro']
for v in variants:
 d=w/v;d.mkdir(exist_ok=True)
 data=subprocess.check_output(['git','archive','v1.0.0-rc2:archives/1.0.0-rc2/source/core'],cwd=r)
 with tarfile.open(fileobj=io.BytesIO(data)) as t:t.extractall(d,filter='data')
 for patch in ['common-fixtures-over-rc2.patch']+([] if v=='control' else [v+'.patch']):
  run(['git','apply','--binary',str(e/patch)],cwd=d)
 run(['go','test','-tags=itomtest','-c','-o',str(w/(v+'.test')),'./tunnel'],cwd=d)
if a.build_only:raise SystemExit(0)
results=[]
# Do not run other compile/performance jobs concurrently. All errors are retained.
for trial,order in enumerate([variants,list(reversed(variants))],1):
 for rtt in [5,40]:
  for v in order:
   env=dict(os.environ,ITOM_LOAD_CLIENT_RTT_MS=str(rtt),ITOM_EXPERIMENT_LABEL=v)
   f=w/f'trial{trial}-{v}-rtt{rtt}.log'
   with f.open('w') as out:cp=subprocess.run([str(w/(v+'.test')),'-test.run','^TestV110IdleLightReference$','-test.v','-test.timeout','150s'],env=env,stdout=out,stderr=subprocess.STDOUT)
   results.append({'trial':trial,'variant':v,'client_rtt_ms':rtt,'returncode':cp.returncode,'log':f.name})
(w/'exit-status.json').write_text(json.dumps(results,indent=2)+'\n')
raise SystemExit(int(any(x['returncode'] for x in results)))
