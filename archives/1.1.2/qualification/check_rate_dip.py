"""Qualify intact HTTPS and session recovery across a 100/1/100 Mbps link."""
from pathlib import Path
import argparse,json
p=argparse.ArgumentParser();p.add_argument('directory',type=Path);a=p.parse_args()
def read(n):return json.loads((a.directory/n).read_text())
d,m=read('candidate.json'),read('manifest.json')
assert m['client_rate_dip']=={'high_mbps':100,'low_mbps':1,'after_seconds':10,'duration_seconds':15}
assert m['mobile_imap_latency_ms']==[30,55,100,70] and m['application_probe_timeout_seconds']==30
assert m['provider_shared_mbps']['client_both_directions']==100
assert (m['duration'],m['cycles'],m['streams'])==('20s',2,4)
assert d['success'] and d['stage']=='complete' and len(d['Phases'])==4,d.get('error')
assert all(p['success']for p in d['Phases'])
mail=d['diagnostics']['Mail'];assert mail['failed_accounts']==0 and mail['ready_accounts']==mail['writer_accounts']==mail['data_accounts']==10
assert not d['diagnostics']['Session']['closed'] and d['diagnostics']['Client']['ActiveLines']==d['diagnostics']['Client']['TotalLines']==30
assert d['Provider']['connection_rejections']==0
assert max(d['IdleMS']+d['AfterMS'])<=300
rates=[{'direction':p['name'],'mbps':p[p['name']]['full_phase_mbps']}for p in d['Phases']]
assert min(p['mbps']for p in rates)>0
assert min(p['mbps']for p in rates[2:])>=30,rates
result={'verified':True,'link_mbps':[100,1,100],'dip_seconds':15,'client_imap_path_rtt_ms':[30,55,100,70],'application_echo_deadline_seconds':30,'all_four_https_phases_integrity_checked':True,'all_thirty_lines_remain_active':True,'phase_mbps':rates,'max_idle_post_ms':max(d['IdleMS']+d['AfterMS']),'max_loaded_ms':max(d['LoadedMS']),'field_qualified':False}
(a.directory/'RATE_DIP_VALIDATION.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result,indent=2))
