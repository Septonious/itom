"""Targeted normal 1.0.0 recovery checks, in isolation after Android build."""
from pathlib import Path
import os,subprocess
W=Path('/workspace/work/v1');S=W/'repository/archives/1.0.0/source';GO='/workspace/toolchains/go/bin/go'
e=os.environ.copy();e.update(GOTOOLCHAIN='local',GOWORK='off',GOFLAGS='-mod=readonly',GOMAXPROCS='2',GOCACHE='/workspace/toolchains/go-cache',GOPATH='/workspace/toolchains/gopath')
assert (W/'build-complete.json').exists()
pattern='^(TestRev11|TestRev10Discovery|TestRev7CorrelatedControlEOFBetweenTransfersKeepsEveryCarrier|TestRev7CappedMixedSocketsRepairWithoutLosingOriginalStream)'
with (W/'final-recovery.jsonl').open('w') as log:
 subprocess.run([GO,'test','-json','./tunnel','-run',pattern,'-count=1','-timeout=150s'],cwd=S/'core',env=e,stdout=log,stderr=subprocess.STDOUT,check=True)
print('Normal brief stall, MULTI ramp, bounded blackhole, correlated EOF and post-load recovery passed')
