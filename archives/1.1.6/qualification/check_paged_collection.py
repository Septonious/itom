"""Verify complete TLS transfers and audited capacity reuse under a mutation byte budget."""
import argparse,json
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('directory',type=Path);a=p.parse_args()
d=json.loads((a.directory/'candidate.json').read_text());m=json.loads((a.directory/'manifest.json').read_text())
assert m['max_expunge_mib']==4
assert d['success'] and d['stage']=='complete' and len(d['Phases'])==4,d.get('error')
assert all(p['success']for p in d['Phases'])
assert not d['diagnostics']['Session']['closed'] and d['diagnostics']['Mail']['failed_accounts']==0
assert d['diagnostics']['Client']['ActiveLines']==d['diagnostics']['Client']['TotalLines']==30
assert d['Provider']['connection_rejections']==0
assert d['Provider']['commands'].get('REJECTED_OVERSIZED_EXPUNGE',0)>0
assert d['Provider']['commands']['MAX_EXPUNGE_BYTES'] > 4<<20
assert 1<<20 < d['Provider']['commands']['MAX_ACCEPTED_EXPUNGE_BYTES'] <= 4<<20
assert d['diagnostics']['Mail']['retired_segments']>0
assert max(d['IdleMS']+d['AfterMS'])<=300 and max(d['LoadedMS'])<=2000
server_retired=0
last_server=None
for name in ('client','server'):
 rows=[]
 for f in a.directory.glob('candidate-'+name+'-trace.jsonl*'):
  for line in f.read_text().splitlines():
   row=json.loads(line)
   if row.get('Mail'):rows.append(row)
 closing=False
 for row in sorted(rows,key=lambda r:r.get('SampledAtMS',0)):
  session=row.get('Session')
  if isinstance(session,dict) and session['closed']:
   # After successful post-probes and the open final diagnostics above, the
   # benchmark cancels its process context. Do not call that shutdown a live
   # carrier failure. Only a terminal suffix with normal close reasons qualifies.
   closing=True
   assert all(x.get('failure','')in ('','MAIL_CARRIER_CLOSED')for x in row['Mail']['lines'])
   continue
  assert not closing,'open runtime snapshot after terminal close'
  assert row['Mail']['failed_accounts']==0
  assert sum(bool(x['housekeeping'])for x in row['Mail']['lines'])<=2
  if name=='server':
   server_retired=max(server_retired,row['Mail']['retired_segments']);last_server=row['Mail']
assert server_retired>0 and last_server is not None
assert not last_server['reclamation']['paused']
assert not d['diagnostics']['Mail']['reclamation']['paused']
r={'verified':True,'client_retired_segments':d['diagnostics']['Mail']['retired_segments'],'server_retired_segments':server_retired,'initial_oversized_refusals':d['Provider']['commands']['REJECTED_OVERSIZED_EXPUNGE'],'max_accepted_retry_mime_bytes':d['Provider']['commands']['MAX_ACCEPTED_EXPUNGE_BYTES'],'max_initial_attempt_mime_bytes':d['Provider']['commands']['MAX_EXPUNGE_BYTES'],'full_phase_100mbps':d['targets']['full_phase_100mbps'],'max_idle_post_ms':max(d['IdleMS']+d['AfterMS']),'max_loaded_ms':max(d['LoadedMS']),'field_qualified':False}
(a.directory/'PAGED_COLLECTION_VALIDATION.json').write_text(json.dumps(r,indent=2)+'\n');print(json.dumps(r,indent=2))
