"""Verify thirty roles with five empty accounts and stalled missing-name lookups."""
import argparse,json
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('directory',type=Path);a=p.parse_args()
def read(n):return json.loads((a.directory/n).read_text())
d,m=read('candidate.json'),read('manifest.json')
assert m['fresh_control_accounts']==5 and m['missing_control_delay_ms']==30000 and m['minimum_ready_accounts']==0
assert d['success'] and d['stage']=='complete' and len(d['Phases'])==4 and all(x['success']for x in d['Phases'])
assert d['startup_ms']<10000
diag=d['diagnostics'];assert not diag['Session']['closed'] and diag['Mail']['failed_accounts']==0
assert diag['Client']['ActiveLines']==diag['Client']['TotalLines']==30
assert diag['Mail']['ready_accounts']==diag['Mail']['writer_accounts']==diag['Mail']['data_accounts']==10
c=d['Provider']['commands'];assert c.get('MISSING_CONTROL_SELECT',0)==c.get('MISSING_CONTROL_STATUS',0)==0
assert c.get('DELAYED_OVERSIZED_EXPUNGE',0)==0 and c['MAX_ACCEPTED_EXPUNGE_BYTES']<=16<<20
assert d['Provider']['connection_rejections']==0 and not diag['Mail']['reclamation']['paused']
assert max(d['IdleMS']+d['AfterMS'])<=300 and max(d['LoadedMS'])<=2000
phases=[{'direction':x['name'],'mbps':x[x['name']]['full_phase_mbps']}for x in d['Phases']]
assert all(x['mbps']>=100 for x in phases),phases
result={'verified':True,'initial_empty_accounts':5,'missing_name_stall_ms':30000,'missing_select_status_commands':0,'active_roles':30,'startup_ms':d['startup_ms'],'phases':phases,'field_qualified':False}
(a.directory/'MIXED_STARTUP_VALIDATION.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result,indent=2))
