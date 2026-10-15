from pathlib import Path
import os,subprocess
w=Path('/workspace/work/rev5')
for attempt in range(1,5):
 d=w/'ramp-debug'/str(attempt);d.mkdir(exist_ok=True)
 e=os.environ.copy();e.update(GOTOOLCHAIN='local',GOWORK='off',GOFLAGS='-mod=readonly',GOMAXPROCS='2',GOCACHE='/workspace/toolchains/go-cache',GOPATH='/workspace/toolchains/gopath',ITOM_LOAD_DIAGNOSTICS='1',ITOM_LOAD_TRACE_DIR=str(d))
 with (d/'run.jsonl').open('w') as out:
  r=subprocess.run(['/workspace/toolchains/go/bin/go','test','-json','-count=1','-timeout=100s','./tunnel','-run','^TestRev5RampAbortAndRestartKeepOriginalChatUsable$'],cwd=w/'source/itom/core',env=e,stdin=subprocess.DEVNULL,stdout=out,stderr=subprocess.STDOUT)
 print(attempt,r.returncode,flush=True)
 if r.returncode:break
