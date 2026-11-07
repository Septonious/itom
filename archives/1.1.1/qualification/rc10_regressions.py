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
(a.reference/'mailstream/reclamation_lease_test.go').write_text((source/'mailstream/reclamation_lease_test.go').read_text())
results={}
for label,root,pattern,expected in [
 ('rc9_feedback',a.reference,'^TestUnfinishedHistoryDoesNotCreateIdleControlFeedback$','idle housekeeping sustained its own reporting loop'),
 ('rc9_append_drain_lease',a.reference,'^TestSlowAppendDrainDoesNotOwnSharedCleanupLease$','slow APPEND replies held the shared mutation lease'),
 ('candidate_append_drain_lease',source,'^TestSlowAppendDrainDoesNotOwnSharedCleanupLease$',None),
 ('candidate_feedback_and_pressure',source,'^Test(UnfinishedHistory|PeerPressure|CurrentDataBacklog)',False),
 ('candidate_lifecycle',source,'^TestAutomaticControlAndAbandonedSessionCleanup$',False)]:
 r=subprocess.run(['go','test','-json','-count=1','-timeout=180s','-run',pattern,'.'],cwd=root/'mailstream',stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True,timeout=240)
 (a.out/(label+'.jsonl')).write_text(r.stdout)
 if expected:assert r.returncode!=0 and expected in r.stdout,r.stdout[-2000:]
 else:assert r.returncode==0,r.stdout[-4000:]
 results[label]={'verified':True,'exit_status':r.returncode,'expected_reference_failure':bool(expected)}
(a.out/'REGRESSION_RESULTS.json').write_text(json.dumps(results,indent=2)+'\n')
print(json.dumps(results,indent=2))
