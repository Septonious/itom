from pathlib import Path
import json,os,subprocess,sys,time
W=Path('/workspace/work/rev8');S=W/'repository/archives/rev8/source';C=Path('/workspace/work/rev3/build-cache');GO='/workspace/toolchains/go/bin/go'
sys.path.insert(0,str(S/'tools'))
from build_core import build_binaries,runtime_hash
from transport_qualification import source_fingerprint
env=os.environ.copy()
for k in list(env):
 if k.startswith(('ITOM_LOAD_','ITOM_FIELD_','ITOM_REV4_TEST_')) or k in ('ITOM_REWORK_QUALIFICATION','ITOM_CORE_QUALIFICATION','ITOM_UNSTABLE_SURVIVAL','ITOM_STABILITY_QUALIFICATION'):del env[k]
env.update(GOTOOLCHAIN='local',GOWORK='off',GOFLAGS='-mod=readonly',GOMAXPROCS='2',GOCACHE='/workspace/toolchains/go-cache',GOPATH='/workspace/toolchains/gopath')
env['PATH']='/workspace/toolchains/go/bin:'+env['PATH'];identity=source_fingerprint();runtime=runtime_hash(S/'core')
def run(label,args,cwd=S,selected=env,required=True):
 print('RUN',label,flush=True);start=time.monotonic()
 with (W/(label+'.log')).open('w')as log:r=subprocess.run(args,cwd=cwd,env=selected,stdout=log,stderr=subprocess.STDOUT)
 print('RESULT',label,r.returncode,'seconds',round(time.monotonic()-start,1),flush=True)
 assert source_fingerprint()==identity,'Sources changed during build'
 if required and r.returncode:raise RuntimeError(label+' failed; see private log')
 return r.returncode
load_exit=1
(S/'dist').mkdir(exist_ok=True)
def command(args,cwd,capture=False):
 with (W/'final-binaries.log').open('a')as log:r=subprocess.run([GO,*args],cwd=cwd,env=env,stdout=subprocess.PIPE if capture else log,stderr=log,text=True,check=True)
 return r.stdout if capture else ''
build_binaries(command,S/'core',S/'dist',env,release='Rev8');print('RESULT linux-binaries PASS',flush=True)
jdk=next((C/'jdk17').rglob('bin/javac')).parents[1];aenv=env.copy();aenv.update(JAVA_HOME=str(jdk),GRADLE_USER_HOME=str(C/'gradle-home'),ANDROID_USER_HOME=str(C/'android-user'));aenv['PATH']=str(jdk/'bin')+':'+aenv['PATH'];aenv['JAVA_TOOL_OPTIONS']=aenv.get('JAVA_TOOL_OPTIONS','')+' '+(C/'java-options.txt').read_text()
run('final-android',['python3','tools/build.py','android','--cache',str(C),'--expect-revision','transport-rework3-rev8','--accept-android-licenses'],selected=aenv)
assert runtime_hash(S/'core')==runtime
(W/'build-complete.json').write_text(json.dumps({'source_sha256':identity,'runtime_sha256':runtime,'performance_exit_code':load_exit,'android_build_passed':True,'linux_binaries_built':True},indent=2)+'\n')
print('READY FOR PACKAGING; performance failure is never waived',flush=True)
