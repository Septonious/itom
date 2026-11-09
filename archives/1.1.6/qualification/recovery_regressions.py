"""Reproduce1.1.1's lost control retry and blocked repeated HTTPS under pressure."""
from pathlib import Path
import argparse,json,subprocess
p=argparse.ArgumentParser();p.add_argument('--reference',type=Path,required=True);p.add_argument('--out',type=Path,required=True);a=p.parse_args()
source=Path(__file__).resolve().parents[1]/'source';a.out.mkdir(parents=True,exist_ok=False)
for n in ('recovery_quarantine_test.go','recovery_https_test.go','proactive_recovery_test.go','recovery_lease_idle_test.go','historical_page_test.go'):(a.reference/'mailstream'/n).write_bytes((source/'mailstream'/n).read_bytes())
results={}
for label,pattern,cause in [('quarantine','TestRecoveryResolvesControlQuarantineAboveFrozenStartupFence','pending control quarantine was skipped'),('https','TestHTTPSRecoversAfterLiveControlFailureAndNewStoragePressure','blocked while recovered retention stayed charged'),('proactive','TestInheritedDataDebtRequestsRecoveryWithBulkHeadroom','inherited DATA recovery priority'),('idle_lease','TestRecoveryPreferenceDoesNotReserveAnIdleMutationLease','idle mutation lease was reserved'),('historical_page','TestHistoricalDataUsesBoundedHealthyPagesAndConservativeRetries','historical page ignored healthy/retry ceiling')]:
 for version,root in [('v111',a.reference),('candidate',source)]:
  r=subprocess.run(['go','test','-json','-count=1','-timeout=90s','-run','^'+pattern+'$','.'],cwd=root/'mailstream',capture_output=True,text=True,timeout=120)
  raw=r.stdout+r.stderr;key=version+'_'+label;(a.out/(key+'.jsonl')).write_text(raw)
  if version=='v111':assert r.returncode!=0 and cause in raw,raw[-4000:]
  else:assert r.returncode==0,raw[-4000:]
  results[key]={'verified':True,'exit_status':r.returncode,'expected_reference_failure':version=='v111'}
(a.out/'REGRESSION_RESULTS.json').write_text(json.dumps(results,indent=2)+'\n');print(json.dumps(results,indent=2))
