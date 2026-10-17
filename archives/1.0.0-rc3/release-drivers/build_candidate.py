from pathlib import Path
import json,os,subprocess,sys,time
W=Path('/workspace/work/rc3');S=W/'repository/archives/1.0.0-rc3/source';C=Path('/workspace/work/rev3/build-cache');GO='/workspace/toolchains/go/bin/go'
sys.path.insert(0,str(S/'tools'));from build_core import build_binaries,runtime_hash
from transport_qualification import source_fingerprint
e=os.environ.copy();e.update(GOTOOLCHAIN='local',GOWORK='off',GOFLAGS='-mod=readonly',GOMAXPROCS='2',GOCACHE='/workspace/toolchains/go-cache',GOPATH='/workspace/toolchains/gopath');e['PATH']='/workspace/toolchains/go/bin:'+e['PATH'];identity=source_fingerprint();runtime=runtime_hash(S/'core');(S/'dist').mkdir(exist_ok=True)
def command(args,cwd,capture=False):
 with (W/'binaries.log').open('a')as log:r=subprocess.run([GO,*args],cwd=cwd,env=e,stdout=subprocess.PIPE if capture else log,stderr=log,text=True,check=True)
 return r.stdout if capture else ''
command(['vet','./tunnel','./datapath','./mailbox','./adminserver'],S/'core');build_binaries(command,S/'core',S/'dist',e,release='RC3');print('Linux AMD64/ARM64 built; targeted vet passed',flush=True)
jdk=next((C/'jdk17').rglob('bin/javac')).parents[1];e.update(JAVA_HOME=str(jdk),GRADLE_USER_HOME=str(C/'gradle-home'),ANDROID_USER_HOME=str(C/'android-user'));e['PATH']=str(jdk/'bin')+':'+e['PATH'];e['JAVA_TOOL_OPTIONS']=e.get('JAVA_TOOL_OPTIONS','')+' '+(C/'java-options.txt').read_text()
with (W/'android-build.log').open('w')as log:r=subprocess.run(['python3','tools/build.py','android','--cache',str(C),'--expect-revision','itom-1-0-0-rc3','--accept-android-licenses'],cwd=S,env=e,stdout=log,stderr=subprocess.STDOUT)
print('Android exit',r.returncode,flush=True)
if r.returncode:raise SystemExit(r.returncode)
assert identity==source_fingerprint() and runtime==runtime_hash(S/'core')
(W/'build-complete.json').write_text(json.dumps({'source_sha256':identity,'runtime_sha256':runtime,'android_build_passed':True,'linux_binaries_built':True},indent=2)+'\n')
print('READY for final isolated measurement and packaging',flush=True)
