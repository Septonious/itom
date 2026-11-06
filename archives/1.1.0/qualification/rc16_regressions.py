"""Reproduce RC15's mobile bulk/window equilibrium on a frozen source tree."""
import argparse,json,subprocess
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--reference',type=Path,required=True);p.add_argument('--out',type=Path,required=True);a=p.parse_args()
source=Path(__file__).resolve().parents[1]/'source';a.out.mkdir(parents=True,exist_ok=False)
for name in ('delivery_mobile_test.go','download_batch_test.go'):
 (a.reference/'mailstream'/name).write_text((source/'mailstream'/name).read_text())
results={}
for version,root in [('rc15',a.reference),('candidate',source)]:
 r=subprocess.run(['go','test','-json','-count=1','-timeout=30s','-run','^Test(MobileBulkEscapesSmallTrafficBaseline|ClientDownloadAmortizesDelayedFetchWithoutWaitingForBatch)$','.'],cwd=root/'mailstream',stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True,timeout=120)
 (a.out/(version+'.jsonl')).write_text(r.stdout)
 if version=='rc15':assert r.returncode!=0 and 'mobile bulk stuck below capacity' in r.stdout and '5.1/80 Mbps' in r.stdout and 'download still pays a slow FETCH' in r.stdout,r.stdout[-3000:]
 else:assert r.returncode==0,r.stdout[-3000:]
 results[version]={'verified':True,'exit_status':r.returncode,'expected_reference_failure':version=='rc15'}
(a.out/'REGRESSION_RESULTS.json').write_text(json.dumps(results,indent=2)+'\n');print(json.dumps(results,indent=2))
