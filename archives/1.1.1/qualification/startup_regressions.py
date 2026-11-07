"""Reproduce missing-folder bootstrap and feedback bias on immutable 1.1.0."""
import argparse,json,subprocess
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--reference',type=Path,required=True);p.add_argument('--out',type=Path,required=True);a=p.parse_args()
source=Path(__file__).resolve().parents[1]/'source';a.out.mkdir(parents=True,exist_ok=False)
# Share generated TLS fixtures, never change the frozen transport implementation.
for n in ('application_test.go','missing_bootstrap_test.go','feedback_availability_test.go'):
 (a.reference/'mailstream'/n).write_bytes((source/'mailstream'/n).read_bytes())
results={}
for label,pattern,cause in [('missing_folders','TestFirstFiveEmptyAccountsBootstrapWithoutMissingFolderSelection','control folders did not bootstrap'),('feedback_rotation','TestFeedbackBurstsRotateAcrossAvailableAccounts','receipt publications; expected')]:
 for version,root in [('v110',a.reference),('candidate',source)]:
  r=subprocess.run(['go','test','-json','-count=1','-timeout=45s','-run','^'+pattern+'$','.'],cwd=root/'mailstream',capture_output=True,text=True,timeout=120)
  raw=r.stdout+r.stderr;key=version+'_'+label;(a.out/(key+'.jsonl')).write_text(raw)
  if version=='v110':assert r.returncode!=0 and cause in raw,raw[-4000:]
  else:assert r.returncode==0,raw[-4000:]
  results[key]={'verified':True,'exit_status':r.returncode,'expected_reference_failure':version=='v110'}
(a.out/'REGRESSION_RESULTS.json').write_text(json.dumps(results,indent=2)+'\n');print(json.dumps(results,indent=2))
