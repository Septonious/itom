"""Replay frozen RC12 selected audits, global quarantine and physical storage reuse."""
import argparse,json,subprocess
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--reference',type=Path,required=True);p.add_argument('--out',type=Path,required=True);a=p.parse_args()
source=Path(__file__).resolve().parents[1]/'source';a.out.mkdir(parents=True,exist_ok=False)
for name in ('selected_audit_test.go','quarantine_field_test.go'):
 (a.reference/'mailstream'/name).write_text((source/'mailstream'/name).read_text())
results={}
for label,root,pattern,expected in [
 ('rc12_selected_audit',a.reference,'^TestReclamationAuditsSelectedMailboxWithoutStatus$','audited collection failed'),
 ('rc12_global_quarantine',a.reference,'^TestIndependentPhysicalAccountCollectsAfterPeerCleanupTimeout$','globally blocked'),
 ('candidate_storage_reuse',source,'^Test(ReclamationAuditsSelectedMailboxWithoutStatus|IndependentPhysicalAccountCollectsAfterPeerCleanupTimeout|RepeatedHTTPSExceedsPhysicalBudgetWithPortableCollection)$',None)]:
 r=subprocess.run(['go','test','-json','-count=1','-timeout=100s','-run',pattern,'.'],cwd=root/'mailstream',stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True,timeout=180)
 (a.out/(label+'.jsonl')).write_text(r.stdout)
 if expected:assert r.returncode!=0 and expected in r.stdout,r.stdout[-4000:]
 else:assert r.returncode==0,r.stdout[-4000:]
 results[label]={'verified':True,'exit_status':r.returncode,'expected_reference_failure':bool(expected)}
(a.out/'REGRESSION_RESULTS.json').write_text(json.dumps(results,indent=2)+'\n');print(json.dumps(results,indent=2))
