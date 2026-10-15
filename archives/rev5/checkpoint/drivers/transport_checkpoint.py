from pathlib import Path
import os,sys,json,subprocess,time
W=Path('/workspace/work/rev5'); S=W/'source/itom'; GO='/workspace/toolchains/go/bin/go'
sys.path.insert(0,str(S/'tools'))
from transport_qualification import source_fingerprint
fp=source_fingerprint(); out=W/'transport-checkpoint-results';out.mkdir(exist_ok=True)
e=os.environ.copy()
for k in list(e):
 if k.startswith(('ITOM_LOAD_','ITOM_FIELD_','ITOM_REV4_TEST_')) or k in ('ITOM_REWORK_QUALIFICATION','ITOM_CORE_QUALIFICATION','ITOM_UNSTABLE_SURVIVAL'):del e[k]
e.update(GOTOOLCHAIN='local',GOWORK='off',GOFLAGS='-mod=readonly',GOMAXPROCS='2',GOCACHE='/workspace/toolchains/go-cache',GOPATH='/workspace/toolchains/gopath')
r={'source_sha256':fp,'complete':False,'steps':[]}
for name,command in [
 ('normal',[sys.executable,'tools/field_qualification.py','--go',GO,'--repeats','1','--results',str(out/'normal')]),
 ('commit-delay',[sys.executable,'tools/field_qualification.py','--go',GO,'--repeats','1','--commit-delay-ms','12','--results',str(out/'commit-delay')]),
 ('speed',[sys.executable,'tools/transport_qualification.py','--go',GO,'--accounts','15','--streams','both','--repeats','3','--uid-history','64','--results',str(out/'speed')])]:
 print('RUN',name,flush=True);assert source_fingerprint()==fp
 t=time.monotonic()
 with (out/(name+'.log')).open('w') as f:code=subprocess.run(command,cwd=S,env=e,stdin=subprocess.DEVNULL,stdout=f,stderr=subprocess.STDOUT).returncode
 ok=code==0 and source_fingerprint()==fp;r['steps'].append(dict(name=name,passed=ok,exit_code=code,elapsed_seconds=round(time.monotonic()-t,3)))
 (out/'status.json').write_text(json.dumps(r,indent=2)+'\n');print('RESULT',name,ok,flush=True)
 if not ok:raise SystemExit(1)
r['complete']=True;(out/'status.json').write_text(json.dumps(r,indent=2)+'\n')
