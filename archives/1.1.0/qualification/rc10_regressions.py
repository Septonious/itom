"""Reproduce RC9 idle reporting and verify pressure/deferral safety invariants."""
import argparse,json,subprocess
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--reference',type=Path,required=True);p.add_argument('--out',type=Path,required=True);a=p.parse_args()
source=Path(__file__).resolve().parents[1]/'source'
a.out.mkdir(parents=True,exist_ok=False)
# Add only this regression test to the frozen source, never product code.
test=(source/'mailstream/quiet_maintenance_test.go').read_text()
body=test[test.index('// RC9 kept'):test.index('func TestPeerPressure')]
(a.reference/'mailstream/quiet_feedback_regression_test.go').write_text('package mailstream\nimport("testing";"time")\n'+body)
results={}
for label,root,pattern,expected in [
 ('rc9_feedback',a.reference,'^TestUnfinishedHistoryDoesNotCreateIdleControlFeedback$',True),
 ('candidate_feedback_and_pressure',source,'^Test(UnfinishedHistory|PeerPressure|CurrentDataBacklog)',False),
 ('candidate_lifecycle',source,'^TestAutomaticControlAndAbandonedSessionCleanup$',False)]:
 r=subprocess.run(['go','test','-json','-count=1','-timeout=180s','-run',pattern,'.'],cwd=root/'mailstream',stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True,timeout=240)
 (a.out/(label+'.jsonl')).write_text(r.stdout)
 if expected:assert r.returncode!=0 and 'idle housekeeping sustained its own reporting loop' in r.stdout,r.stdout[-2000:]
 else:assert r.returncode==0,r.stdout[-4000:]
 results[label]={'verified':True,'exit_status':r.returncode,'expected_reference_failure':expected}
(a.out/'REGRESSION_RESULTS.json').write_text(json.dumps(results,indent=2)+'\n')
print(json.dumps(results,indent=2))
