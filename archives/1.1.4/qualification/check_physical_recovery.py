"""Require repeat 120+ Mbps after an allowed slower cold upload with real old mailbox bytes and captured debt distribution."""
import argparse,json
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('directory',type=Path);a=p.parse_args()
def read(n):return json.loads((a.directory/n).read_text())
d,m=read('candidate.json'),read('manifest.json');amounts=[int(x)for x in m['recovered_client_bytes'].split(',')]
assert m['physical_recovered_history'] and m['download_first'] and len(amounts)==10
assert m['duration']=='20s' and m['cycles']==3 and m['streams']==16
assert d['initial_recovered_client_bytes']==sum(amounts)
assert d['Provider']['commands']['SEEDED_HISTORY_BYTES']==sum(max(0,x-(1<<20))for x in amounts)
assert d['success'] and d['stage']=='complete' and len(d['Phases'])==8,d.get('error')
assert all(p['success']for p in d['Phases'])
assert [p['name']for p in d['Phases'][:6]]==['download','upload']*3
assert d['recovery_completed_before_phase']==6
mail=d['diagnostics']['Mail'];assert mail['ready_accounts']==mail['writer_accounts']==mail['data_accounts']==10 and mail['recovered_retention_bytes']==0 and mail['failed_accounts']==0
assert d['diagnostics']['Client']['ActiveLines']==d['diagnostics']['Client']['TotalLines']==30
assert not d['diagnostics']['Session']['closed'] and d['Provider']['connection_rejections']==0
assert max(d['IdleMS']+d['AfterMS'])<=300 and max(d['LoadedMS'])<=2000
phases=[{'direction':p['name'],'mbps':p[p['name']]['full_phase_mbps']}for p in d['Phases']]
# The user explicitly accepted one slower cold run while inherited mail drains.
assert phases[1]['mbps']>=90,phases
assert all(p['mbps']>=120 for i,p in enumerate(phases)if i!=1),phases
for direction in ('upload','download'):
 rates=[p['mbps']for p in phases[:6]if p['direction']==direction];assert min(rates[1:])>=.9*rates[0],rates
r={'verified':True,'minimum_warm_full_phase_mbps':120,'minimum_cold_upload_mbps':90,'cold_upload_exception_user_authorized':True,'aspirational_150mbps':all(p['mbps']>=150 for p in phases),'initial_recovered_bytes':sum(amounts),'physically_seeded_bytes':d['Provider']['commands']['SEEDED_HISTORY_BYTES'],'final_recovered_bytes':0,'phases':phases,'max_idle_post_ms':max(d['IdleMS']+d['AfterMS']),'max_loaded_ms':max(d['LoadedMS']),'field_qualified':False}
(a.directory/'PHYSICAL_RECOVERY_VALIDATION.json').write_text(json.dumps(r,indent=2)+'\n');print(json.dumps(r,indent=2))
