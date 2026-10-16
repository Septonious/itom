"""Targeted final recovery checks; no complete suite/performance claim."""
from pathlib import Path
import json,os,subprocess
W=Path('/workspace/work/rev10');S=W/'repository/archives/rev10/source';GO='/workspace/toolchains/go/bin/go'
e=os.environ.copy();e.update(GOTOOLCHAIN='local',GOWORK='off',GOFLAGS='-mod=readonly',GOMAXPROCS='2',GOCACHE='/workspace/toolchains/go-cache',GOPATH='/workspace/toolchains/gopath')
# Run in isolation after Android compilation. Keep two command outputs so the
# recorded normal log can be reproduced without a full test-matrix run.
assert (W/'build-complete.json').exists()
with (W/'final-recovery.jsonl').open('w') as log:
 for pattern in ['^(TestRev7CorrelatedControlEOFBetweenTransfersKeepsEveryCarrier|TestRev7CappedMixedSocketsRepairWithoutLosingOriginalStream)$','^TestRev10']:
  subprocess.run([GO,'test','-json','./tunnel','-run',pattern,'-count=1','-timeout=120s'],cwd=S/'core',env=e,stdout=log,stderr=subprocess.STDOUT,check=True)
print('Normal post-load, capped-socket and discovery-outage recovery passed')
