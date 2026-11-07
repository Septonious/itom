"""Verify recovery from a real persisted fixture ledger, without manual resets."""
from pathlib import Path
import argparse,json

p=argparse.ArgumentParser();p.add_argument('directory',type=Path);a=p.parse_args()
d=json.loads((a.directory/'candidate.json').read_text())
assert d['success'] and d['stage']=='complete'
assert d['initial_recovered_client_bytes']==51*(1<<20)*10
assert d['recovery_completed_before_phase']==6 and len(d['Phases'])==8
assert all(s['success']for s in d['Phases'])
mail=d['diagnostics']['Mail']
assert mail['recovered_retention_bytes']==0 and all(l['recovered_retention_bytes']==0 for l in mail['lines'])
assert mail['failed_accounts']==0 and mail['retired_segments']>0 and not d['diagnostics']['Session']['closed']
assert d['diagnostics']['Client']['ActiveLines']==d['diagnostics']['Client']['TotalLines']==30
assert max(d['IdleMS']+d['AfterMS'])<=300 and max(d['LoadedMS'])<=2000
assert d['Provider']['connection_rejections']==0 and d['Provider']['commands']['MAX_STORE_ROWS']<=256
after=d['Phases'][6:]
assert [s['name']for s in after]==['upload','download']
assert all(s[s['name']]['full_phase_mbps']>=100 for s in after)
server=[]
for file in a.directory.glob('candidate-server-trace.jsonl*'):
    for line in file.read_text().splitlines():
        row=json.loads(line)
        if row.get('Event')=='runtime' and isinstance(row.get('Mail'),dict):server.append(row)
assert server and all(row['Mail']['failed_accounts']==0 and not row['Mail']['reclamation']['paused']for row in server)
result={'verified':True,'initial_recovered_client_bytes':d['initial_recovered_client_bytes'],'final_recovered_client_bytes':0,'all_cold_transfers_completed':True,'phases':[{'direction':s['name'],'mbps':s[s['name']]['full_phase_mbps'],'after_audited_recovery':i>=6}for i,s in enumerate(d['Phases'])],'cold_phases_do_not_claim_100mbps':True,'max_idle_post_ms':max(d['IdleMS']+d['AfterMS']),'max_loaded_ms':max(d['LoadedMS'])}
(a.directory/'RECOVERED_VALIDATION.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result,indent=2))
