"""Reproduce frozen RC17 publication bursts, stale flights and DNS pressure."""
import argparse,json,subprocess
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--reference',type=Path,required=True);p.add_argument('--out',type=Path,required=True);a=p.parse_args()
source=Path(__file__).resolve().parents[1]/'source';a.out.mkdir(parents=True,exist_ok=False)
for module,name in [('mailstream','pacing_dispatch_test.go'),('native','dns_pressure_test.go')]:
 (a.reference/module/name).write_text((source/module/name).read_text())
results={}
cases=[('bulk_burst','mailstream','MeasuredBulkBurstLeavesInteractivePublicationImmediate','burst published'),('idle_flight','mailstream','NewBulkBurstDoesNotReusePriorSpeedtestAllowance','idle burst retained unaudited'),('cached_dns','native','CachedDNSBurstPreservesTTLIDsAndAvoidsNetworkAndStorageWrites','cached A answer repeated'),('dns_workers','native','AnsweredDNSBurstReleasesWorkerBeforeNextBatch','answered DNS worker retained')]
for label,module,pattern,expected in cases:
 for version,root in [('rc17',a.reference),('candidate',source)]:
  r=subprocess.run(['go','test','-json','-count=1','-timeout=30s','-run','^Test'+pattern+'$','.'],cwd=root/module,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True,timeout=120)
  key=version+'_'+label;(a.out/(key+'.jsonl')).write_text(r.stdout)
  if version=='rc17':assert r.returncode!=0 and expected in r.stdout,r.stdout[-3000:]
  else:assert r.returncode==0,r.stdout[-3000:]
  results[key]={'verified':True,'exit_status':r.returncode,'expected_reference_failure':version=='rc17'}
(a.out/'REGRESSION_RESULTS.json').write_text(json.dumps(results,indent=2)+'\n');print(json.dumps(results,indent=2))
