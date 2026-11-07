#!/usr/bin/env python3
import os,subprocess,json,sys,time
from pathlib import Path
import argparse
p=argparse.ArgumentParser();p.add_argument('--go',default='go');p.add_argument('--results',type=Path,default=Path('reports/rev3-matrix'));args=p.parse_args()
root=Path(__file__).resolve().parents[2];sys.path.insert(0,str(root/'tools'))
from rev2_transport_matrix import summarize,fingerprint
out=args.results.resolve();out.mkdir(parents=True,exist_ok=False)
env={k:v for k,v in os.environ.items()if not k.startswith('ITOM_')};env.update(GOPROXY=os.environ.get('GOPROXY','off'),GOTOOLCHAIN='local',GOMAXPROCS='2',ITOM_LONG_LOAD='1',ITOM_LOAD_SUPERVISORS='1',ITOM_LOAD_SECONDS='20',ITOM_LOAD_WARMUP_SECONDS='5',ITOM_LOAD_COMMAND_DELAY_MS='80',ITOM_LOAD_PROVIDER_MBPS='600',ITOM_LOAD_REPEATS='1',ITOM_LOAD_DIAGNOSTICS='0',ITOM_LOAD_TRACE='0',ITOM_SPEEDTEST_GATE='1',ITOM_SPEEDTEST_VALIDATE='0')
report={'classification':'LABORATORY_DIAGNOSTIC','field_qualified':False,'cases':[]}
cases=[(10,1,'uniform',32768),(10,8,'uniform',32768),(15,1,'uniform',32768),(15,8,'uniform',32768),(15,24,'uniform',32768),(15,24,'uniform-fragmented',4096),(15,24,'wrapper-regular',32768)]
for accounts,streams,profile,chunk in cases:
 name=f'{accounts}-{streams}-{profile}';case_env=env|{'ITOM_LOAD_ACCOUNTS':str(accounts),'ITOM_LOAD_STREAMS':str(streams),'ITOM_LOAD_CHUNK_BYTES':str(chunk)}
 if profile=='wrapper-regular':case_env.update(ITOM_LOAD_COMMAND_DELAY_MS='0',ITOM_FIELD_PROVIDER_MODEL='1',ITOM_FIELD_LATENCY_PROFILE='rev1-wrapper',ITOM_FIELD_WRAPPER_MAX_US='500000',ITOM_LOAD_SERVER_COMMIT_US='12000',ITOM_LOAD_CLIENT_COMMIT_US='500')
 before=fingerprint(root);log=out/(name+'.jsonl');print('RUN',name,flush=True)
 with log.open('w')as f:r=subprocess.run([args.go,'test','-tags=itomtest','-json','-count=1','-timeout=240s','./tunnel','-run','^TestLoadedApplicationProductionHarness$'],cwd=root/'core',env=case_env,stdout=f,stderr=subprocess.STDOUT)
 after=fingerprint(root);case={'name':name,'accounts':accounts,'streams':streams,'profile':profile,'chunk_bytes':chunk,'exit_code':r.returncode,'source_before':before,'source_after':after,'log':log.name,'measurement':summarize(log)};assert before==after
 report['cases'].append(case);(out/'results.json').write_text(json.dumps(report,indent=2)+'\n');print('RESULT',name,r.returncode,case['measurement']['results'],flush=True)
