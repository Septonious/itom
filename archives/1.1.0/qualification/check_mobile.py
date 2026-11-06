"""Validate repeated asymmetric mobile transfers; retain rates rather than claim 100 Mbps on an 80 Mbps path."""
import argparse,json
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('directory',type=Path);p.add_argument('--reference',type=Path,required=True);a=p.parse_args()
d=json.loads((a.directory/'candidate.json').read_text());ref=json.loads((a.reference/'candidate.json').read_text())
assert d['success'] and d['stage']=='complete' and len(d['Phases'])==4,d.get('error')
assert all(x['success']for x in d['Phases'])
assert not d['diagnostics']['Session']['closed'] and d['diagnostics']['Mail']['failed_accounts']==0
assert d['diagnostics']['Client']['ActiveLines']==d['diagnostics']['Client']['TotalLines']==30
assert d['Provider']['connection_rejections']==0 and d['Provider']['commands']['DELAYED_DATA_FETCH']>0
assert max(d['IdleMS']+d['AfterMS'])<=300 and max(d['LoadedMS'])<=2000
phases=[]
for i,x in enumerate(d['Phases']):
 name=x['name'];speed=x[name]['full_phase_mbps'];old=ref['Phases'][i][name]['full_phase_mbps']
 assert speed >= (45 if name=='upload' else 40),(name,speed)
 assert speed >= old*1.5,(name,speed,old)
 phases.append({'direction':name,'full_phase_mbps':speed,'reference_full_phase_mbps':old,'improvement':speed/old,'half_second_mbps':x[name]['half_second_mbps']})
for i in (2,3):assert phases[i]['full_phase_mbps']>=phases[i-2]['full_phase_mbps']*.8,phases
result={'verified':True,'model':'95 Mbps up / 80 Mbps down; separate 500/600 ms DATA FETCH callbacks; 60 ms APPEND service; variable 10–50/20–40 ms network delay','max_idle_post_ms':max(d['IdleMS']+d['AfterMS']),'max_loaded_ms':max(d['LoadedMS']),'phases':phases,'field_qualified':False}
(a.directory/'MOBILE_VALIDATION.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result,indent=2))
