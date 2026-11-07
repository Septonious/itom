"""Reproduce RC8's decoder/cleanup deadlocks, then verify the candidate fixes."""
import argparse,json,os,subprocess,shutil
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--reference',type=Path,required=True);p.add_argument('--out',type=Path,required=True);a=p.parse_args()
source=Path(__file__).resolve().parents[1]/'source';a.out.mkdir(parents=True,exist_ok=True)
# Only tests are added to the frozen reference. Its implementation is untouched.
for rel in ['mailstream/cleanup_burst_test.go','core/third_party/go-imap/imapclient/close_backpressure_test.go']:
 shutil.copy2(Path(__file__).resolve().parent/'fixtures/rc8/cleanup_burst_test.go' if rel=='mailstream/cleanup_burst_test.go' else source/rel,a.reference/rel)
cases=[('rc8_cleanup',a.reference/'mailstream','.', 'TestRetainedControlCleanupWithEarlyExpungeBurst','retained control cleanup did not finish'),('rc8_close',a.reference/'core/third_party/go-imap','./imapclient','TestCloseInterruptsUndrainedExpungeBurst','Close is stuck behind an undrained EXPUNGE'),('candidate_cleanup',source/'mailstream','.','TestRetainedControlCleanupWithEarlyExpungeBurst',None),('candidate_close',source/'core/third_party/go-imap','./imapclient','TestCloseInterruptsUndrainedExpungeBurst',None),('candidate_manager',source/'core','./manager','TestSystemctlHonorsDeadlineInsteadOfHangingMenu',None)]
results={}
for name,cwd,target,test,expected in cases:
 command=['go','test','-json','-count=1','-timeout=10s','-run','^'+test+'$',target]
 run=subprocess.run(command,cwd=cwd,text=True,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,timeout=180)
 (a.out/(name+'.jsonl')).write_text(run.stdout)
 events=[]
 for line in run.stdout.splitlines():
  try:events.append(json.loads(line))
  except json.JSONDecodeError:pass
 if expected:
  assert run.returncode!=0 and expected in run.stdout and any(e.get('Action')=='fail' and e.get('Test')==test for e in events),name+': expected field failure was not reproduced'
 else:
  assert run.returncode==0 and any(e.get('Action')=='pass' and e.get('Test')==test for e in events),name+': candidate regression failed'
 results[name]={'verified':True,'exit_status':run.returncode,'test':test,'expected_reference_failure':expected is not None}
(a.out/'REGRESSION_RESULTS.json').write_text(json.dumps(results,indent=2)+'\n')
print(json.dumps(results,indent=2))
