"""Reproduce RC14 short-burst, TLS classification and cleanup-preparation faults."""
import argparse,json,subprocess
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--reference',type=Path,required=True);p.add_argument('--out',type=Path,required=True);a=p.parse_args()
source=Path(__file__).resolve().parents[1]/'source';a.out.mkdir(parents=True,exist_ok=False)
for name in ('short_burst_test.go','cleanup_capacity_test.go','tls_burst_test.go','interactive_burst_test.go'):
 (a.reference/'mailstream'/name).write_text((source/'mailstream'/name).read_text())
results={}
cases=[('short_ramp','ShortRepeatedTransfersReachCapacityBeforeTheyEnd','ramp=false'),('concurrent_drains','ConcurrentCleanupDrainsLeaveOtherWritersAvailable','optional cleanup excluded all three'),('tls_record_burst','SmallTLSRecordBurstDoesNotConsumeInteractiveReserve','TLS-sized burst flooded interactive'),('interactive_duplication','InteractiveBurstAvoidsFullPoolDuplicationButIsolatedRequestHedges','still mirrored every publication')]
for label,name,expected in cases:
 for version,root in [('rc14',a.reference),('candidate',source)]:
  r=subprocess.run(['go','test','-json','-count=1','-timeout=30s','-run','^Test'+name+'$','.'],cwd=root/'mailstream',stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True,timeout=120)
  key=version+'_'+label;(a.out/(key+'.jsonl')).write_text(r.stdout)
  if version=='rc14':assert r.returncode!=0 and expected in r.stdout,r.stdout[-3000:]
  else:assert r.returncode==0,r.stdout[-3000:]
  results[key]={'verified':True,'exit_status':r.returncode,'expected_reference_failure':version=='rc14'}
(a.out/'REGRESSION_RESULTS.json').write_text(json.dumps(results,indent=2)+'\n');print(json.dumps(results,indent=2))
