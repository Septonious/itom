"""Prove service lifecycle cannot clear debt without physical collection."""
from pathlib import Path
import argparse,json,subprocess
p=argparse.ArgumentParser();p.add_argument('--reference',type=Path,required=True);p.add_argument('--out',type=Path,required=True);a=p.parse_args()
source=Path(__file__).resolve().parents[1]/'source';a.out.mkdir(parents=True,exist_ok=False)
(a.reference/'core/manager/lifecycle_capacity_test.go').write_bytes((source/'core/manager/lifecycle_capacity_test.go').read_bytes())
# Map the renamed API to precisely the old lifecycle behavior, without changing it.
(a.reference/'core/manager/qualification_lifecycle_api_test.go').write_text('package manager\nfunc VerifyCapacityState(state string) error { return ResetCapacityState(state) }\n')
results={}
for name,root in [('v111',a.reference),('candidate',source)]:
 r=subprocess.run(['go','test','-json','-count=1','-timeout=90s','-run','^TestServiceLifecycleCannotRefundUncollectedMailboxDebt$','./manager'],cwd=root/'core',capture_output=True,text=True,timeout=120)
 raw=r.stdout+r.stderr;(a.out/(name+'.jsonl')).write_text(raw)
 if name=='v111':assert r.returncode!=0 and 'forgave uncollected mailbox debt'in raw,raw[-3000:]
 else:assert r.returncode==0,raw[-3000:]
 results[name]={'verified':True,'exit_status':r.returncode,'expected_reference_failure':name=='v111'}
(a.out/'REGRESSION_RESULTS.json').write_text(json.dumps(results,indent=2)+'\n');print(json.dumps(results,indent=2))
