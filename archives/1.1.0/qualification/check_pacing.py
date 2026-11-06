"""Match constrained TLS transfers, recording probe variation separately from Ookla jitter."""
import argparse,json,statistics
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('directory',type=Path);p.add_argument('--reference',type=Path,required=True);a=p.parse_args()
def read(path):return json.loads(path.read_text())
d,ref=read(a.directory/'candidate.json'),read(a.reference/'candidate.json')
m,old=read(a.directory/'manifest.json'),read(a.reference/'manifest.json')
for key in ('client_upload_mbps','client_download_mbps','append_service_ms','duration','cycles','streams','variable_latency','max_expunge_mib','prefill_control','upload_fetch_delay_ms','download_fetch_delay_ms'):
 assert m[key]==old[key],key
assert (m['client_upload_mbps'],m['client_download_mbps'],m['append_service_ms'],m['duration'],m['cycles'],m['streams'])==(36,80,60,'20s',2,16)
def quantile(v,f):return sorted(v)[int(f*(len(v)-1))]
def stats(result):
 v=result['LoadedMS'];delta=[abs(b-a)for a,b in zip(v,v[1:])]
 return {'median_loaded_ms':statistics.median(v),'p95_loaded_ms':quantile(v,.95),'max_loaded_ms':max(v),'median_adjacent_probe_delta_ms':statistics.median(delta),'p95_adjacent_probe_delta_ms':quantile(delta,.95),'max_idle_post_ms':max(result['IdleMS']+result['AfterMS']),'client_peak_queued_bytes':result['Provider']['client_peak_queued_bytes'],'server_peak_queued_bytes':result['Provider']['server_peak_queued_bytes']}
for result in (d,ref):
 assert result['success'] and result['stage']=='complete' and len(result['Phases'])==4,result.get('error')
 assert all(x['success']for x in result['Phases'])
 assert not result['diagnostics']['Session']['closed'] and result['diagnostics']['Mail']['failed_accounts']==0
 assert result['diagnostics']['Client']['ActiveLines']==result['diagnostics']['Client']['TotalLines']==30
 assert result['Provider']['connection_rejections']==0
 assert max(result['IdleMS']+result['AfterMS'])<=300 and max(result['LoadedMS'])<=2000
current,prior=stats(d),stats(ref)
for key,ratio in [('median_loaded_ms',.85),('p95_loaded_ms',.9),('median_adjacent_probe_delta_ms',.8)]:
 assert current[key]<=prior[key]*ratio,(key,current[key],prior[key])
phases=[]
for x,y in zip(d['Phases'],ref['Phases']):
 name=x['name'];speed=x[name]['full_phase_mbps'];before=y[name]['full_phase_mbps']
 assert y['name']==name and speed>=before*.9,(name,speed,before)
 if name=='upload':assert speed>=20,speed
 phases.append({'direction':name,'mbps':speed,'reference_mbps':before})
paced={}
for side in ('client','server'):
 rows=[]
 for file in a.directory.glob('candidate-'+side+'-trace.jsonl*'):
  rows.extend(json.loads(line)for line in file.read_text().splitlines())
 rows=[r for r in rows if isinstance(r.get('Session'),dict)]
 assert rows and all(r['Mail']['failed_accounts']==0 and not r['Mail']['reclamation']['paused']for r in rows)
 assert max(sum(bool(l['housekeeping'])for l in r['Mail']['lines'])for r in rows)<=2
 paced[side]=max(r['Session'].get('bulk_pacing_deferrals',0)for r in rows)
 assert paced[side]>0
result={'verified':True,'model':'36/80 Mbps directional wire rates, 60 ms APPEND service, variable delay, 16 streams, two 20-second upload/download cycles','candidate':current,'reference_rc17':prior,'phases':phases,'pacing_deferrals':paced,'probe_variation_is_not_ookla_jitter':True,'field_qualified':False}
(a.directory/'PACING_VALIDATION.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result,indent=2))
