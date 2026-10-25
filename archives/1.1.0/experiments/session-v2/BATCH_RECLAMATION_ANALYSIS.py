import json,re,statistics
from pathlib import Path
root=Path(__file__).resolve().parent
def duration(value):
 units={'ns':1e-6,'µs':1e-3,'ms':1,'s':1000,'m':60000,'h':3600000}
 return sum(float(n)*units[u] for n,u in re.findall(r'([0-9.]+)(ns|µs|ms|s|m|h)',value))
def parse(name):
 cases=[];active={}
 for l in (root/name).read_text().splitlines():
  r=json.loads(l);t=r.get('Test','');action=r.get('Action')
  if not t or '/' in t:continue
  if action=='run':active[t]={'test':t,'http_ms':[],'mailboxes':[]}
  if action=='output':
   out=r.get('Output','');c=active.get(t)
   if c is None:continue
   m=re.search(r'HTTP integrity sample run=(\d+) payload_bytes=(\d+) elapsed=(\S+)',out)
   if m:c['http_ms'].append({'run':int(m[1]),'payload_bytes':int(m[2]),'ms':duration(m[3])})
   m=re.search(r'physical mailbox plane=(\d+) live=(\d+) peak=(\d+) total_uids=(\d+) bytes=(\d+).*server_reclamation=\{(.*?)\}',out)
   if m:
    c['mailboxes'].append({'plane':int(m[1]),'live':int(m[2]),'peak':int(m[3]),'total_uids':int(m[4]),'bytes':int(m[5]),'reported_cleanup':{k:int(v) for k,v in re.findall(r'(\w+):(\d+)',m[6])}})
  if action in ['pass','fail']:
   c=active.pop(t,{'test':t});c.update(outcome=action,elapsed_seconds=r.get('Elapsed'));cases.append(c)
 return cases
baseline=parse('BATCH_RECLAMATION_MATCHED_BASELINE.jsonl');candidate=parse('BATCH_RECLAMATION_MATCHED_CANDIDATE.jsonl')
summary={'meaning':'Matched cold integrity and scripted command-service comparison, not calibrated field RTT or sustained-bandwidth qualification. Pending cleanup counts are last worker diagnostic snapshots, not separately sampled steady-state occupancy.','baseline':baseline,'candidate':candidate,'by_case':{}}
for test in sorted({c['test'] for c in baseline+candidate}):
 compare={}
 for label,cases in [('baseline',baseline),('candidate',candidate)]:
  matched=[c for c in cases if c['test']==test]
  compare[label]={'outcomes':[c['outcome'] for c in matched],'case_seconds':[c['elapsed_seconds'] for c in matched],
   'fast_peaks':[b['peak'] for c in matched for b in c.get('mailboxes',[]) if b['plane']==0],
   'fast_cleanup_pending_reported':[b['reported_cleanup'].get('Pending') for c in matched for b in c.get('mailboxes',[]) if b['plane']==0]}
 summary['by_case'][test]=compare
(root/'BATCH_RECLAMATION_MATCHED_SUMMARY.json').write_text(json.dumps(summary,indent=2)+'\n')
for test,c in summary['by_case'].items():print(test,json.dumps(c))
