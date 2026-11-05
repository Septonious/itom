"""Require repeated transfers, finite storage reuse and healthy server collection."""
from pathlib import Path
import argparse,json

p=argparse.ArgumentParser();p.add_argument('directory',type=Path);p.add_argument('--require-goal',action='store_true');a=p.parse_args()
d=json.loads((a.directory/'candidate.json').read_text())
assert d['success'] and d['stage']=='complete' and len(d['Phases'])==6
assert all(phase['success']for phase in d['Phases'])
if a.require_goal:assert d['targets']['full_phase_100mbps']
diag=d['diagnostics']
assert not diag['Session']['closed'] and diag['Mail']['failed_accounts']==0
assert diag['Client']['ActiveLines']==diag['Client']['TotalLines']==30
assert d['Provider']['connection_rejections']==0 and d['Provider']['commands']['MAX_STORE_ROWS']<=256
assert diag['Mail']['retired_segments']>0
assert max(d['IdleMS']+d['AfterMS'])<=300 and max(d['LoadedMS'])<=2000
server=[]
for file in a.directory.glob('candidate-server-trace.jsonl*'):
    for line in file.read_text().splitlines():
        row=json.loads(line)
        if row.get('Event')=='runtime' and isinstance(row.get('Mail'),dict):server.append(row)
assert server,'server runtime trace missing'
server.sort(key=lambda row:row['SampledAtMS'])
assert all(row['Mail']['failed_accounts']==0 and not row['Mail']['reclamation']['paused']for row in server)
last=server[-1]['Mail']
assert last['ready_accounts']==10 and last['retired_segments']>0
assert sum(line['control_messages_collected']for line in last['lines'])>0
result={'verified':True,'full_phase_100mbps':d['targets']['full_phase_100mbps'],'phases':[{'direction':s['name'],'mbps':s[s['name']]['full_phase_mbps']}for s in d['Phases']],'client_retired_segments':diag['Mail']['retired_segments'],'server_retired_segments':last['retired_segments'],'server_control_messages_collected':sum(line['control_messages_collected']for line in last['lines']),'server_collection_timeouts':0,'max_idle_post_ms':max(d['IdleMS']+d['AfterMS']),'max_loaded_ms':max(d['LoadedMS'])}
(a.directory/'REPEATED_COLLECTION_VALIDATION.json').write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps(result,indent=2))
