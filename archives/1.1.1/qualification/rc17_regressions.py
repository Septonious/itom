"""Reproduce oversized and non-resumable RC16 collection on a frozen source tree."""
import argparse,json,subprocess
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--reference',type=Path,required=True);p.add_argument('--out',type=Path,required=True);a=p.parse_args()
source=Path(__file__).resolve().parents[1]/'source';a.out.mkdir(parents=True,exist_ok=False)
for name in ('collection_page_test.go','housekeeping_snapshot_test.go'):
 (a.reference/'mailstream'/name).write_text((source/'mailstream'/name).read_text())
results={}
for label,pattern,expected in [('byte_budget','ByteBoundedDataCollectionRetainsDebtUntilFinalAudit','bounded DATA collection failed'),('partial_resume','DataCollectionResumesAfterPartialUnknownOutcome','partial DATA outcome cannot resume'),('history_byte_fence','HistoricalCollectionPagesRespectConsumedUIDFence','historical DATA ignored byte page'),('coherent_snapshot','HousekeepingSnapshotUsesOneOwnershipInstant','snapshot reported')]:
 for version,root in [('rc16',a.reference),('candidate',source)]:
  r=subprocess.run(['go','test','-json','-count=1','-timeout=30s','-run','^Test'+pattern+'$','.'],cwd=root/'mailstream',stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True,timeout=120)
  key=version+'_'+label;(a.out/(key+'.jsonl')).write_text(r.stdout)
  if version=='rc16':assert r.returncode!=0 and expected in r.stdout,r.stdout[-3000:]
  else:assert r.returncode==0,r.stdout[-3000:]
  results[key]={'verified':True,'exit_status':r.returncode,'expected_reference_failure':version=='rc16'}
(a.out/'REGRESSION_RESULTS.json').write_text(json.dumps(results,indent=2)+'\n');print(json.dumps(results,indent=2))
