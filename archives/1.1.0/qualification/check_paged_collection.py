"""Verify complete TLS transfers and audited capacity reuse under a mutation byte budget."""
import argparse,json
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('directory',type=Path);a=p.parse_args()
d=json.loads((a.directory/'candidate.json').read_text());m=json.loads((a.directory/'manifest.json').read_text())
assert m['max_expunge_mib']==16
assert d['success'] and d['stage']=='complete' and len(d['Phases'])==4,d.get('error')
assert all(p['success']for p in d['Phases']) and d['targets']['full_phase_100mbps']
assert not d['diagnostics']['Session']['closed'] and d['diagnostics']['Mail']['failed_accounts']==0
assert d['diagnostics']['Client']['ActiveLines']==d['diagnostics']['Client']['TotalLines']==30
assert d['Provider']['connection_rejections']==0
assert d['Provider']['commands'].get('REJECTED_OVERSIZED_EXPUNGE',0)==0
assert 1<<20 < d['Provider']['commands']['MAX_EXPUNGE_BYTES'] <= 16<<20
assert d['diagnostics']['Mail']['retired_segments']>0
assert max(d['IdleMS']+d['AfterMS'])<=300 and max(d['LoadedMS'])<=2000
server_retired=0
for name in ('client','server'):
 for f in a.directory.glob('candidate-'+name+'-trace.jsonl*'):
  for line in f.read_text().splitlines():
   row=json.loads(line)
   if not row.get('Mail'):continue
   assert row['Mail']['failed_accounts']==0 and not row['Mail']['reclamation']['paused']
   assert sum(bool(x['housekeeping'])for x in row['Mail']['lines'])<=2
   if name=='server':server_retired=max(server_retired,row['Mail']['retired_segments'])
assert server_retired>0
r={'verified':True,'client_retired_segments':d['diagnostics']['Mail']['retired_segments'],'server_retired_segments':server_retired,'max_expunge_mime_bytes':d['Provider']['commands']['MAX_EXPUNGE_BYTES'],'max_idle_post_ms':max(d['IdleMS']+d['AfterMS']),'max_loaded_ms':max(d['LoadedMS']),'field_qualified':False}
(a.directory/'PAGED_COLLECTION_VALIDATION.json').write_text(json.dumps(r,indent=2)+'\n');print(json.dumps(r,indent=2))
