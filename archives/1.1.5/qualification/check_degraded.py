"""Check useful repeated throughput with five unavailable and five healthy accounts."""
import argparse,json
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('directory',type=Path);a=p.parse_args()
def read(n):return json.loads((a.directory/n).read_text())
d,m=read('candidate.json'),read('manifest.json')
assert m['accounts']==10 and m['unavailable_control_accounts']==m['minimum_ready_accounts']==5
assert d['success'] and d['stage']=='complete' and len(d['Phases'])==4 and all(x['success']for x in d['Phases'])
diag=d['diagnostics'];mail=diag['Mail'];assert not diag['Session']['closed'] and mail['failed_accounts']==0
assert diag['Client']['ActiveLines']==15 and diag['Client']['TotalLines']==30
assert mail['ready_accounts']==mail['writer_accounts']==mail['data_accounts']==5
for l in mail['lines']:
 healthy=int(l['account'][1:])>5
 assert l['ready']==l['writer']==l['data']==healthy
 if healthy:assert l['startup_attempts']==1
c=d['Provider']['commands'];assert c.get('UNAVAILABLE_CONTROL_LIST',0)>0
assert c.get('DELAYED_OVERSIZED_EXPUNGE',0)==0 and c['MAX_ACCEPTED_EXPUNGE_BYTES']<=16<<20
assert d['Provider']['connection_rejections']==0 and not mail['reclamation']['paused']
assert max(d['IdleMS']+d['AfterMS'])<=300 and max(d['LoadedMS'])<=2000
phases=[{'direction':x['name'],'mbps':x[x['name']]['full_phase_mbps']}for x in d['Phases']]
assert all(x['mbps']>=40 for x in phases),phases
result={'verified':True,'configured_accounts':10,'healthy_accounts':5,'active_roles':15,'total_roles':30,'minimum_full_phase_mbps':40,'phases':phases,'field_qualified':False}
(a.directory/'DEGRADED_VALIDATION.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result,indent=2))
