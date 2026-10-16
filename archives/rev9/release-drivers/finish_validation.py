from pathlib import Path
import os,subprocess,time,json,sys
W=Path('/workspace/work/rev9');S=W/'repository/archives/rev9/source';GO='/workspace/toolchains/go/bin/go'
for _ in range(600):
 if (W/'build-complete.json').exists():break
 report=S/'dist/build-report.json'
 if report.exists() and json.loads(report.read_text()).get('status')=='failed':raise SystemExit('Android build failed; final measurement not started')
 time.sleep(2)
else:raise SystemExit('Build did not finish')
e=os.environ.copy();e.update(GOTOOLCHAIN='local',GOWORK='off',GOFLAGS='-mod=readonly',GOMAXPROCS='2',GOCACHE='/workspace/toolchains/go-cache',GOPATH='/workspace/toolchains/gopath');sys.path.insert(0,str(S/'tools'));from transport_qualification import source_fingerprint
assert source_fingerprint()==json.loads((W/'build-complete.json').read_text())['source_sha256']
for name,args in [('final-correctness',[GO,'test','-race','-json','-count=1','-timeout=90s','./wire','./datapath','./mailbox','./servercfg','./manager','./adminserver','./tunnel','-run','^TestRev9']),('final-recovery',[GO,'test','-json','-count=1','-timeout=90s','./tunnel','-run','^TestRev7CorrelatedControlEOFBetweenTransfersKeepsEveryCarrier$|^TestRev7ControlAndReceiveRepairBypassQueuedOptionalLogins$'])]:
 print('RUN',name,flush=True)
 with (W/(name+'.jsonl')).open('w')as log:r=subprocess.run(args,cwd=S/'core',env=e,stdout=log,stderr=subprocess.STDOUT)
 print('RESULT',name,r.returncode,flush=True)
 if r.returncode:raise SystemExit('Required correctness check failed: '+name)
print('RUN final low-RTT SINGLE/MULTI; strict 100 useful Mbps; no profiler/build workload',flush=True)
with (W/'final-performance.log').open('w')as log:r=subprocess.run(['python3','tools/transport_qualification.py','--go',GO,'--accounts','15','--streams','both','--seconds','60','--repeats','1','--retained-state','--minimum-mbps','100','--command-delay-ms','20','--results',str(W/'final-performance')],cwd=S,env=e,stdout=log,stderr=subprocess.STDOUT)
print('RESULT final-performance',r.returncode,flush=True)
(W/'validation-complete.json').write_text(json.dumps({'targeted_correctness_passed':True,'performance_exit_code':r.returncode,'source_sha256':source_fingerprint()},indent=2)+'\n')
print('Final measurement recorded without speed waiver; ready to package declared testing candidate',flush=True)
